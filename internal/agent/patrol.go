package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/areming/ops-agent/internal/config"
	"github.com/areming/ops-agent/internal/memory"
	"github.com/areming/ops-agent/internal/safety"
	"github.com/areming/ops-agent/internal/tools"
)

// patrol runs background checks on a timer and performs narrow,
// whitelisted self-heal actions. Reversible whitelisted fixes (restarting
// a watched unit) run automatically and are audited; everything else is
// recorded as a todo for a human, never executed. It holds no connection:
// it can act with no CLI attached.
type patrol struct {
	store *memory.Store
	shell tools.Tool // runs read-only checks and whitelisted restarts
	cfg   config.PatrolConfig
}

func newPatrol(store *memory.Store, cfg config.PatrolConfig) *patrol {
	return &patrol{store: store, shell: tools.Shell{}, cfg: cfg}
}

// finding is one check result. A finding with OK=false is a problem; Unit
// is set when the problem is a down service patrol may try to restart.
type finding struct {
	Check    string `json:"check"`
	OK       bool   `json:"ok"`
	Severity string `json:"severity,omitempty"`
	Title    string `json:"title"`
	Detail   string `json:"detail,omitempty"`
	Unit     string `json:"unit,omitempty"`
}

// Run sweeps once at startup (so a freshly enrolled agent reports quickly)
// then on the configured interval until ctx is cancelled.
func (p *patrol) Run(ctx context.Context) {
	log.Printf("patrol: started (interval=%s checks=%v services=%v)",
		p.cfg.Interval, p.cfg.Checks, p.cfg.Services)
	p.runOnce(ctx)

	ticker := time.NewTicker(p.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.runOnce(ctx)
		}
	}
}

// runOnce executes the enabled checks, acts on or records each problem, and
// persists the sweep.
func (p *patrol) runOnce(ctx context.Context) {
	started := time.Now().UTC()

	var findings []finding
	for _, check := range p.cfg.Checks {
		switch check {
		case "disk":
			findings = append(findings, p.checkDisk(ctx)...)
		case "load":
			findings = append(findings, p.checkLoad(ctx)...)
		case "key_services":
			findings = append(findings, p.checkServices(ctx)...)
		default:
			log.Printf("patrol: unknown check %q, skipping", check)
		}
	}

	for _, f := range findings {
		if f.OK {
			continue
		}
		p.handle(ctx, f)
	}

	p.record(ctx, started, findings)
}

// handle either runs a whitelisted auto-remedy or records a todo.
func (p *patrol) handle(ctx context.Context, f finding) {
	if f.Unit == "" {
		// No safe automatic fix (disk/load): leave it for a human.
		p.todo(ctx, f, suggestedAction(f))
		return
	}

	cmd := "sudo -n systemctl restart " + f.Unit
	if !safety.IsPatrolAutoRemedy(cmd, p.cfg.Services) {
		// Should not happen for a watched unit, but never run a command the
		// gate declines: record it as skipped and leave a todo.
		p.auditSkipped(ctx, cmd)
		p.todo(ctx, f, "restart "+f.Unit+" (skipped: not permitted for unattended remedy)")
		return
	}

	res, err := p.run(ctx, cmd)
	verdict := safety.Verdict{Decision: safety.Allow, Risk: "low", Reversible: true, Reason: "patrol auto-remedy"}
	if err != nil {
		audit(ctx, p.store, "patrol", cmd, verdict, "auto", -1, err.Error())
		p.todo(ctx, f, "auto-restart of "+f.Unit+" failed to launch: "+err.Error())
		log.Printf("patrol: restart %s: %v", f.Unit, err)
		return
	}
	audit(ctx, p.store, "patrol", cmd, verdict, "auto", res.ExitCode, res.Output)
	if res.ExitCode != 0 {
		// The restart command ran but the service did not come back: a human
		// should look, so leave a todo alongside the audit trail.
		p.todo(ctx, f, fmt.Sprintf("auto-restart of %s exited %d; investigate", f.Unit, res.ExitCode))
		log.Printf("patrol: restart %s exited %d", f.Unit, res.ExitCode)
		return
	}
	log.Printf("patrol: auto-restarted %s", f.Unit)
}

// checkDisk flags mounts at or above the configured usage threshold.
func (p *patrol) checkDisk(ctx context.Context) []finding {
	res, err := p.run(ctx, "df -P")
	if err != nil {
		return []finding{probeError("disk", "df -P", err)}
	}
	var out []finding
	for _, u := range parseDiskUsage(res.Output) {
		if u.pct >= p.cfg.DiskPct {
			out = append(out, finding{
				Check:    "disk",
				OK:       false,
				Severity: "high",
				Title:    fmt.Sprintf("disk %d%% full on %s", u.pct, u.mount),
				Detail:   fmt.Sprintf("%s is at %d%% (threshold %d%%)", u.mount, u.pct, p.cfg.DiskPct),
			})
		}
	}
	return out
}

// checkLoad flags when the 1-minute load average per CPU is at or above the
// configured threshold.
func (p *patrol) checkLoad(ctx context.Context) []finding {
	loadRes, err := p.run(ctx, "cat /proc/loadavg")
	if err != nil {
		return []finding{probeError("load", "cat /proc/loadavg", err)}
	}
	load1, err := parseLoadAvg(loadRes.Output)
	if err != nil {
		return []finding{probeError("load", "parse loadavg", err)}
	}
	ncpu := 1
	if nres, err := p.run(ctx, "nproc"); err == nil {
		if n, perr := parseNproc(nres.Output); perr == nil {
			ncpu = n
		}
	}

	per := load1 / float64(ncpu)
	if per < p.cfg.LoadPer {
		return nil
	}
	return []finding{{
		Check:    "load",
		OK:       false,
		Severity: "medium",
		Title:    fmt.Sprintf("high load: %.2f over %d CPU(s)", load1, ncpu),
		Detail:   fmt.Sprintf("1-min load %.2f = %.2f/CPU (threshold %.2f/CPU)", load1, per, p.cfg.LoadPer),
	}}
}

// checkServices flags watched units that are not active. A down unit is a
// candidate for auto-restart (its Unit field is set).
func (p *patrol) checkServices(ctx context.Context) []finding {
	var out []finding
	for _, unit := range p.cfg.Services {
		res, err := p.run(ctx, "systemctl is-active "+unit)
		if err != nil {
			out = append(out, probeError("key_services", "systemctl is-active "+unit, err))
			continue
		}
		state := strings.TrimSpace(res.Output)
		if state == "active" {
			continue
		}
		out = append(out, finding{
			Check:    "key_services",
			OK:       false,
			Severity: "high",
			Title:    fmt.Sprintf("service %s is %s", unit, state),
			Detail:   fmt.Sprintf("systemctl is-active %s reported %q", unit, state),
			Unit:     unit,
		})
	}
	return out
}

// run executes a command through the shell tool.
func (p *patrol) run(ctx context.Context, command string) (tools.Result, error) {
	args, err := json.Marshal(map[string]string{"command": command})
	if err != nil {
		return tools.Result{}, err
	}
	return p.shell.Execute(ctx, args)
}

func (p *patrol) todo(ctx context.Context, f finding, action string) {
	if p.store == nil {
		return
	}
	if _, err := p.store.InsertTodo(ctx, memory.Todo{
		Source:          "patrol",
		Severity:        f.Severity,
		Title:           f.Title,
		Detail:          f.Detail,
		SuggestedAction: action,
	}); err != nil {
		log.Printf("patrol: insert todo: %v", err)
	}
}

func (p *patrol) auditSkipped(ctx context.Context, cmd string) {
	audit(ctx, p.store, "patrol", cmd, safety.Verdict{
		Decision: safety.Confirm, Risk: "high", Reversible: false,
		Reason: "patrol declined unattended remedy",
	}, "skipped", 0, "")
}

func (p *patrol) record(ctx context.Context, started time.Time, findings []finding) {
	if p.store == nil {
		return
	}
	checksJSON, _ := json.Marshal(p.cfg.Checks)
	if findings == nil {
		findings = []finding{}
	}
	findingsJSON, _ := json.Marshal(findings)
	if _, err := p.store.InsertPatrolRun(ctx, memory.PatrolRun{
		StartedAt:    started.Format(time.RFC3339),
		FinishedAt:   time.Now().UTC().Format(time.RFC3339),
		ChecksJSON:   string(checksJSON),
		FindingsJSON: string(findingsJSON),
	}); err != nil {
		log.Printf("patrol: record run: %v", err)
	}
}

// suggestedAction offers a human-facing next step for findings patrol will
// not act on automatically.
func suggestedAction(f finding) string {
	switch f.Check {
	case "disk":
		return "free space or grow the volume; check large logs/caches"
	case "load":
		return "identify the heavy process (top/ps) and investigate"
	default:
		return "investigate"
	}
}

func probeError(check, what string, err error) finding {
	return finding{
		Check:    check,
		OK:       false,
		Severity: "low",
		Title:    "patrol probe failed: " + what,
		Detail:   err.Error(),
	}
}

// diskUsage is one mount's used percentage parsed from `df -P`.
type diskUsage struct {
	mount string
	pct   int
}

// parseDiskUsage reads POSIX `df -P` output. Columns are fixed:
// Filesystem, blocks, Used, Available, Capacity(%), Mounted-on. The header
// line and any malformed rows are skipped.
func parseDiskUsage(output string) []diskUsage {
	var out []diskUsage
	for i, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if i == 0 {
			continue // header
		}
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		pctStr := strings.TrimSuffix(fields[4], "%")
		pct, err := strconv.Atoi(pctStr)
		if err != nil {
			continue
		}
		out = append(out, diskUsage{mount: fields[5], pct: pct})
	}
	return out
}

// parseLoadAvg reads the 1-minute load from /proc/loadavg's first field.
func parseLoadAvg(output string) (float64, error) {
	fields := strings.Fields(output)
	if len(fields) == 0 {
		return 0, fmt.Errorf("empty loadavg")
	}
	return strconv.ParseFloat(fields[0], 64)
}

// parseNproc reads the CPU count printed by `nproc`.
func parseNproc(output string) (int, error) {
	return strconv.Atoi(strings.TrimSpace(output))
}
