package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/areming/ops-agent/internal/config"
	"github.com/areming/ops-agent/internal/memory"
	"github.com/areming/ops-agent/internal/model"
	"github.com/areming/ops-agent/internal/tools"
)

func TestParseDiskUsage(t *testing.T) {
	out := `Filesystem     1024-blocks     Used Available Capacity Mounted on
/dev/sda1         51474044 47000000   4474044      92% /
tmpfs              8159152        0   8159152       0% /dev/shm
/dev/sdb1        103080888 51000000  52080888      50% /data`
	got := parseDiskUsage(out)
	want := []diskUsage{{"/", 92}, {"/dev/shm", 0}, {"/data", 50}}
	if len(got) != len(want) {
		t.Fatalf("got %d mounts, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("mount %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParseLoadAvg(t *testing.T) {
	got, err := parseLoadAvg("3.50 2.10 1.80 2/512 90210\n")
	if err != nil {
		t.Fatalf("parseLoadAvg: %v", err)
	}
	if got != 3.50 {
		t.Errorf("load1 = %v, want 3.50", got)
	}
	if _, err := parseLoadAvg(""); err == nil {
		t.Error("empty loadavg should error")
	}
}

func TestParseNproc(t *testing.T) {
	n, err := parseNproc(" 8 \n")
	if err != nil || n != 8 {
		t.Fatalf("parseNproc = %d, %v; want 8, nil", n, err)
	}
}

// fakeShell returns scripted output per command and records what ran, so a
// patrol test never touches the real system.
type fakeShell struct {
	replies map[string]tools.Result
	ran     []string
}

func (fakeShell) Name() string                   { return "run_command" }
func (fakeShell) Description() string            { return "fake" }
func (fakeShell) Schema() json.RawMessage        { return json.RawMessage(`{"type":"object"}`) }
func (fakeShell) ReadOnly() bool                 { return false }
func (fakeShell) Display(json.RawMessage) string { return "" }
func (s *fakeShell) Execute(_ context.Context, args json.RawMessage) (tools.Result, error) {
	var a struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(args, &a)
	s.ran = append(s.ran, a.Command)
	if r, ok := s.replies[a.Command]; ok {
		return r, nil
	}
	return tools.Result{}, nil
}

func newTestPatrol(t *testing.T, cfg config.PatrolConfig, shell *fakeShell) (*patrol, *memory.Store) {
	t.Helper()
	store, err := memory.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return &patrol{store: store, shell: shell, cfg: cfg, checks: buildChecks(cfg)}, store
}

// On a finding with no auto-fix, diagnosis investigates, declines a
// write the model proposes (recording it skipped), and stores the model's
// analysis as the todo's suggested action.
func TestPatrolDiagnosisRecordsAnalysisAsTodo(t *testing.T) {
	executed := false
	store, err := memory.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	eng := &engine{reg: tools.NewRegistry(stubTool{executed: &executed}), store: store}

	diag := &fakeProvider{rounds: [][]model.ChatEvent{
		{{Type: model.EventToolCall, Tool: &model.ToolCall{ID: "c1", Name: "do_thing", Arguments: json.RawMessage(`{}`)}}},
		{{Type: model.EventTextDelta, Text: "Root cause: log files filled /. Recommend rotating logs."}},
	}}
	p := &patrol{eng: eng, diagProv: diag, store: store}
	ctx := context.Background()

	p.diagnose(ctx, finding{Check: "disk", OK: false, Severity: "high",
		Title: "disk 95% full on /", Detail: "/ at 95%"})

	if executed {
		t.Error("diagnosis ran a write action; it should have been declined")
	}
	todos, _ := store.ListOpenTodos(ctx)
	if len(todos) != 1 || !strings.Contains(todos[0].SuggestedAction, "rotating logs") {
		t.Fatalf("want one todo carrying the analysis, got %+v", todos)
	}
	rows, _ := store.RecentAudit(ctx, 10)
	if len(rows) != 1 || rows[0].Decision != "skipped" || rows[0].Source != "patrol" {
		t.Fatalf("want one skipped/patrol audit row, got %+v", rows)
	}
}

// A persistent problem is diagnosed once: with an open todo already present,
// diagnose returns without calling the model.
func TestPatrolDiagnosisDedupes(t *testing.T) {
	store, err := memory.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if _, err := store.InsertTodo(ctx, memory.Todo{
		Source: "patrol", Severity: "high", Title: "disk 95% full on /",
	}); err != nil {
		t.Fatalf("seed todo: %v", err)
	}

	diag := &fakeProvider{rounds: [][]model.ChatEvent{{{Type: model.EventTextDelta, Text: "should not run"}}}}
	p := &patrol{eng: &engine{reg: tools.NewRegistry(), store: store}, diagProv: diag, store: store}

	p.diagnose(ctx, finding{Check: "disk", Title: "disk 95% full on /"})

	if diag.call != 0 {
		t.Error("diagnosis model was called despite an existing open todo")
	}
	todos, _ := store.ListOpenTodos(ctx)
	if len(todos) != 1 {
		t.Errorf("dedup failed: %d open todos, want 1", len(todos))
	}
}

func TestBuildChecks(t *testing.T) {
	got := buildChecks(config.PatrolConfig{
		Checks:   []string{"disk", "bogus", "key_services", "load"},
		DiskPct:  90,
		LoadPer:  2.0,
		Services: []string{"nginx"},
	})
	var names []string
	for _, c := range got {
		names = append(names, c.name())
	}
	want := []string{"disk", "key_services", "load"} // bogus skipped, order preserved
	if !slices.Equal(names, want) {
		t.Fatalf("buildChecks names = %v, want %v", names, want)
	}
}

// A watched unit reported down is auto-restarted: the restart command runs,
// an audit row records decision=auto, and the sweep is persisted.
func TestPatrolAutoRestartsDownUnit(t *testing.T) {
	shell := &fakeShell{replies: map[string]tools.Result{
		"systemctl is-active nginx":       {Output: "inactive\n", ExitCode: 3},
		"sudo -n systemctl restart nginx": {Output: "", ExitCode: 0},
	}}
	p, store := newTestPatrol(t, config.PatrolConfig{
		Interval: 0, Checks: []string{"key_services"}, Services: []string{"nginx"},
	}, shell)
	defer store.Close()
	ctx := context.Background()

	p.runOnce(ctx)

	if !slices.Contains(shell.ran, "sudo -n systemctl restart nginx") {
		t.Fatalf("restart command not run; ran=%v", shell.ran)
	}
	rows, _ := store.RecentAudit(ctx, 10)
	if len(rows) != 1 || rows[0].Decision != "auto" || rows[0].Source != "patrol" {
		t.Fatalf("want one auto/patrol audit row, got %+v", rows)
	}
	// A successful restart leaves no todo.
	todos, _ := store.ListOpenTodos(ctx)
	if len(todos) != 0 {
		t.Errorf("successful restart left %d todos, want 0", len(todos))
	}
	runs, _ := store.RecentPatrolRuns(ctx, 10)
	if len(runs) != 1 {
		t.Errorf("patrol_runs = %d, want 1", len(runs))
	}
}

// A disk-full finding has no safe auto-fix: patrol records a todo and runs
// no write command.
func TestPatrolDiskWritesTodoOnly(t *testing.T) {
	shell := &fakeShell{replies: map[string]tools.Result{
		"df -P": {Output: "Filesystem 1024-blocks Used Available Capacity Mounted on\n/dev/sda1 100 95 5 95% /\n"},
	}}
	p, store := newTestPatrol(t, config.PatrolConfig{
		Interval: 0, Checks: []string{"disk"}, DiskPct: 90,
	}, shell)
	defer store.Close()
	ctx := context.Background()

	p.runOnce(ctx)

	todos, _ := store.ListOpenTodos(ctx)
	if len(todos) != 1 || !strings.Contains(todos[0].Title, "95% full on /") {
		t.Fatalf("want one disk todo, got %+v", todos)
	}
	rows, _ := store.RecentAudit(ctx, 10)
	if len(rows) != 0 {
		t.Errorf("disk finding wrote %d audit rows, want 0 (no command run)", len(rows))
	}
	// Only the read-only probe ran.
	if len(shell.ran) != 1 || shell.ran[0] != "df -P" {
		t.Errorf("unexpected commands run: %v", shell.ran)
	}
}
