package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const (
	shellTimeout   = 60 * time.Second
	maxOutputBytes = 16 << 10
)

// Shell runs a shell command and returns its combined output. Its schema
// asks the model to self-assess reversibility and risk so the safety
// gate can combine that with command-pattern rules.
type Shell struct{}

type shellArgs struct {
	Command string `json:"command"`
}

func (Shell) Name() string { return "run_command" }

func (Shell) Description() string {
	return "Run a shell command on the server and return its combined stdout/stderr and exit code. " +
		"Always fill `reversible` and `risk` honestly: the safety gate uses them, together with command rules, " +
		"to decide whether to ask the user before running."
}

func (Shell) ReadOnly() bool { return false }

func (Shell) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "command": {"type": "string", "description": "The shell command to run."},
    "purpose": {"type": "string", "description": "Why you are running it."},
    "reversible": {"type": "boolean", "description": "True if the effect can be undone (e.g. restart a service), false for destructive/irreversible actions (e.g. deleting data)."},
    "risk": {"type": "string", "enum": ["low", "medium", "high"], "description": "Your honest risk assessment of running this command."}
  },
  "required": ["command"]
}`)
}

func (Shell) Display(args json.RawMessage) string {
	var a shellArgs
	_ = json.Unmarshal(args, &a)
	return strings.TrimSpace(a.Command)
}

func (Shell) Execute(ctx context.Context, args json.RawMessage) (Result, error) {
	var a shellArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(a.Command) == "" {
		return Result{}, fmt.Errorf("empty command")
	}

	ctx, cancel := context.WithTimeout(ctx, shellTimeout)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/c", a.Command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", a.Command)
	}

	out, err := cmd.CombinedOutput()
	if cmd.ProcessState == nil {
		// The process never started (e.g. no shell available).
		return Result{}, err
	}
	return Result{
		Output:   truncate(string(out), maxOutputBytes),
		ExitCode: cmd.ProcessState.ExitCode(),
	}, nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n…(truncated)"
}
