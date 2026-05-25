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
	"github.com/areming/ops-agent/internal/model"
	"github.com/areming/ops-agent/internal/safety"
	"github.com/areming/ops-agent/internal/tools"
)

// patrol runs background checks on a timer and performs narrow,
// whitelisted self-heal actions. Reversible whitelisted fixes (restarting
// a watched unit) run automatically and are audited; everything else is
// recorded as a todo for a human, never executed. It holds no connection:
// it can act with no CLI attached.
type patrol struct {
	eng      *engine
	diagProv model.Provider // strong model used to diagnose findings
	store    *memory.Store
	shell    tools.Tool // runs read-only checks and whitelisted restarts
	cfg      config.PatrolConfig
	checks   []check
}

func newPatrol(eng *engine, diagProv model.Provider, cfg config.PatrolConfig) *patrol {
	return &patrol{
		eng:      eng,
		diagProv: diagProv,
		store:    eng.store,
		shell:    tools.Shell{},
		cfg:      cfg,
		checks:   buildChecks(cfg),
	}
}

// check is one patrol probe. It runs read-only commands through run and
// returns findings; a finding with OK=false is a problem worth acting on.
type check interface {
	name() string
	run(ctx context.Context, run runner) []finding
}

// runner executes one command (through the shell tool) on a check's behalf.
type runner func(ctx context.Context, command string) (tools.Result, error)

// buildChecks instantiates the checks named in cfg.Checks, skipping any
// unknown name so a typo disables one check rather than the whole sweep.
func buildChecks(cfg config.PatrolConfig) []check {
	var checks []check
	for _, name := range cfg.Checks {
		switch name {
		case "disk":
			checks = append(checks, diskCheck{pct: cfg.DiskPct})
		case "load":
			checks = append(checks, loadCheck{per: cfg.LoadPer})
		case "key_services":
			checks = append(checks, servicesCheck{units: cfg.Services})
		default:
			log.Printf("patrol: unknown check %q, skipping", name)
		}
	}
	return checks
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
	for _, c := range p.checks {
		findings = append(findings, c.run(ctx, p.run)...)
	}

	for _, f := range findings {
		if f.OK {
			continue
		}
		p.handle(ctx, f)
	}

	p.record(ctx, started, findings)
}

// handle either runs a whitelisted auto-remedy or diagnoses and records a
// finding it will not fix unattended.
func (p *patrol) handle(ctx context.Context, f finding) {
	if f.Unit == "" {
		// No safe automatic fix (disk/load): diagnose and leave it for a human.
		p.diagnose(ctx, f)
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

// diskCheck flags mounts at or above the configured usage threshold.
type diskCheck struct{ pct int }

func (diskCheck) name() string { return "disk" }

func (c diskCheck) run(ctx context.Context, run runner) []finding {
	res, err := run(ctx, "df -P")
	if err != nil {
		return []finding{probeError("disk", "df -P", err)}
	}
	var out []finding
	for _, u := range parseDiskUsage(res.Output) {
		if u.pct >= c.pct {
			out = append(out, finding{
				Check:    "disk",
				OK:       false,
				Severity: "high",
				Title:    fmt.Sprintf("disk %d%% full on %s", u.pct, u.mount),
				Detail:   fmt.Sprintf("%s is at %d%% (threshold %d%%)", u.mount, u.pct, c.pct),
			})
		}
	}
	return out
}

// loadCheck flags when the 1-minute load average per CPU is at or above the
// configured threshold.
type loadCheck struct{ per float64 }

func (loadCheck) name() string { return "load" }

func (c loadCheck) run(ctx context.Context, run runner) []finding {
	loadRes, err := run(ctx, "cat /proc/loadavg")
	if err != nil {
		return []finding{probeError("load", "cat /proc/loadavg", err)}
	}
	load1, err := parseLoadAvg(loadRes.Output)
	if err != nil {
		return []finding{probeError("load", "parse loadavg", err)}
	}
	ncpu := 1
	if nres, err := run(ctx, "nproc"); err == nil {
		if n, perr := parseNproc(nres.Output); perr == nil {
			ncpu = n
		}
	}

	per := load1 / float64(ncpu)
	if per < c.per {
		return nil
	}
	return []finding{{
		Check:    "load",
		OK:       false,
		Severity: "medium",
		Title:    fmt.Sprintf("high load: %.2f over %d CPU(s)", load1, ncpu),
		Detail:   fmt.Sprintf("1-min load %.2f = %.2f/CPU (threshold %.2f/CPU)", load1, per, c.per),
	}}
}

// servicesCheck flags watched units that are not active. A down unit is a
// candidate for auto-restart (its Unit field is set).
type servicesCheck struct{ units []string }

func (servicesCheck) name() string { return "key_services" }

func (c servicesCheck) run(ctx context.Context, run runner) []finding {
	var out []finding
	for _, unit := range c.units {
		res, err := run(ctx, "systemctl is-active "+unit)
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
	if exists, err := p.store.OpenTodoExists(ctx, f.Title); err != nil {
		log.Printf("patrol: check existing todo: %v", err)
	} else if exists {
		return // already recorded; don't spam a new todo every sweep
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

// diagnosisSystemPrompt steers the diagnosis turn: investigate read-only,
// recommend (don't perform) fixes, and end with an actionable summary.
const diagnosisSystemPrompt = "You are opsagent's patrol diagnostician. A background check found a problem on the server. " +
	"Use read-only shell commands to investigate, identify the most likely root cause, and recommend a concrete fix. " +
	"You cannot perform write or destructive actions during patrol — recommend them instead. " +
	"Finish with a brief, actionable summary an operator can act on."

// diagnose investigates a finding that has no safe automatic fix and records
// the model's analysis as a todo. It is skipped (no model call) when an open
// todo for the same finding already exists, so a persistent problem is
// diagnosed once rather than every sweep.
func (p *patrol) diagnose(ctx context.Context, f finding) {
	if p.store != nil {
		if exists, _ := p.store.OpenTodoExists(ctx, f.Title); exists {
			return
		}
	}
	action := suggestedAction(f)
	if p.eng != nil && p.diagProv != nil {
		if analysis := p.runDiagnosis(ctx, f); analysis != "" {
			action = analysis
		}
	}
	p.todo(ctx, f, action)
}

// runDiagnosis drives a connectionless agent turn with the diagnosis model
// and returns its analysis text. The throwaway session is never persisted,
// so diagnosis does not pollute the chat thread.
func (p *patrol) runDiagnosis(ctx context.Context, f finding) string {
	prompt := fmt.Sprintf(
		"A background check reported a problem on this server.\nCheck: %s\nTitle: %s\nDetail: %s\n\n"+
			"Investigate with read-only commands, determine the likely root cause, and recommend a concrete fix.",
		f.Check, f.Title, f.Detail)
	sess := newSession(nil, 0)
	sess.addUser(ctx, prompt)
	ia := &patrolInteraction{store: p.store}
	if err := p.eng.runTurn(ctx, p.diagProv, diagnosisSystemPrompt, ia, sess); err != nil {
		log.Printf("patrol: diagnosis turn: %v", err)
	}
	return strings.TrimSpace(ia.text.String())
}

// patrolInteraction is the connectionless interaction for diagnosis: it
// accumulates the model's text, refuses (recording the decline) any action
// that needs confirmation, and logs the rest.
type patrolInteraction struct {
	store *memory.Store
	text  strings.Builder
}

func (*patrolInteraction) source() string { return "patrol" }

func (ia *patrolInteraction) onDelta(text string) error {
	ia.text.WriteString(text)
	return nil
}

func (*patrolInteraction) onToolStart(tool, command string) {
	log.Printf("patrol diagnosis: ▶ %s: %s", tool, command)
}

func (*patrolInteraction) onError(msg string) {
	log.Printf("patrol diagnosis: %s", msg)
}

func (*patrolInteraction) confirm(string, string, safety.Verdict) (bool, error) {
	return false, nil // no human attached; never auto-run a flagged write
}

func (ia *patrolInteraction) declineRun(ctx context.Context, command string, v safety.Verdict) string {
	audit(ctx, ia.store, "patrol", command, v, "skipped", 0, "")
	return "This action needs a human and will not run during patrol. " +
		"Describe it as a recommendation in your summary instead."
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
	names := make([]string, len(p.checks))
	for i, c := range p.checks {
		names[i] = c.name()
	}
	checksJSON, _ := json.Marshal(names)
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
