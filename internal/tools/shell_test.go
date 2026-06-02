package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestShellMetadata(t *testing.T) {
	var s Shell
	if s.Name() != "run_command" {
		t.Errorf("Name() = %q, want run_command", s.Name())
	}
	if s.ReadOnly() {
		t.Error("ReadOnly() = true, want false (run_command can change state)")
	}
	if !json.Valid(s.Schema()) {
		t.Error("Schema() is not valid JSON")
	}
}

func TestShellDisplayTrims(t *testing.T) {
	got := Shell{}.Display(json.RawMessage(`{"command":"  ls -la  "}`))
	if got != "ls -la" {
		t.Errorf("Display() = %q, want %q", got, "ls -la")
	}
}

func TestShellExecuteSuccess(t *testing.T) {
	// `echo hi` works under both sh -c and cmd /c.
	res, err := Shell{}.Execute(context.Background(), json.RawMessage(`{"command":"echo hi"}`))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", res.ExitCode)
	}
	if !strings.Contains(res.Output, "hi") {
		t.Errorf("Output = %q, want it to contain %q", res.Output, "hi")
	}
}

func TestShellExecuteStreamsOutput(t *testing.T) {
	// A multi-line command's output should reach the sink as it is produced,
	// and the final Result must still hold the full combined output. `echo`
	// chained with `&&` works under both sh -c and cmd /c.
	var streamed strings.Builder
	sink := func(p []byte) { streamed.Write(p) }
	ctx := WithOutputSink(context.Background(), sink)

	res, err := Shell{}.Execute(ctx, json.RawMessage(`{"command":"echo a && echo b && echo c"}`))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", res.ExitCode)
	}
	for _, want := range []string{"a", "b", "c"} {
		if !strings.Contains(streamed.String(), want) {
			t.Errorf("sink did not receive %q; got %q", want, streamed.String())
		}
		if !strings.Contains(res.Output, want) {
			t.Errorf("Result.Output missing %q; got %q", want, res.Output)
		}
	}
}

func TestShellExecuteNoSink(t *testing.T) {
	// With no sink installed (e.g. patrol's path), streaming is simply skipped
	// and the command still runs and reports its output.
	res, err := Shell{}.Execute(context.Background(), json.RawMessage(`{"command":"echo hi"}`))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(res.Output, "hi") {
		t.Errorf("Output = %q, want it to contain %q", res.Output, "hi")
	}
}

func TestShellExecuteNonZeroExit(t *testing.T) {
	// `exit 3` is understood by both sh and cmd. A non-zero exit is a
	// normal result, not a Go error.
	res, err := Shell{}.Execute(context.Background(), json.RawMessage(`{"command":"exit 3"}`))
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil for a command that ran and failed", err)
	}
	if res.ExitCode != 3 {
		t.Errorf("ExitCode = %d, want 3", res.ExitCode)
	}
}

func TestShellExecuteEmptyCommand(t *testing.T) {
	if _, err := (Shell{}).Execute(context.Background(), json.RawMessage(`{"command":"   "}`)); err == nil {
		t.Error("Execute() with blank command = nil error, want error")
	}
}

func TestShellExecuteBadJSON(t *testing.T) {
	if _, err := (Shell{}).Execute(context.Background(), json.RawMessage(`{not json`)); err == nil {
		t.Error("Execute() with invalid JSON = nil error, want error")
	}
}

func TestShellExecuteCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled: the process never starts
	if _, err := (Shell{}).Execute(ctx, json.RawMessage(`{"command":"echo hi"}`)); err == nil {
		t.Error("Execute() with cancelled context = nil error, want error")
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 100); got != "short" {
		t.Errorf("truncate under limit = %q, want unchanged", got)
	}
	long := strings.Repeat("a", 50)
	got := truncate(long, 10)
	if !strings.HasPrefix(got, strings.Repeat("a", 10)) {
		t.Errorf("truncate did not keep the first 10 bytes: %q", got)
	}
	if !strings.HasSuffix(got, "(truncated)") {
		t.Errorf("truncate did not append the marker: %q", got)
	}
}
