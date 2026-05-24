// Package tools holds the capabilities the agent can invoke on the
// model's behalf. Tools are pure executors; the decision of whether an
// action is allowed lives in the safety package, driven by each tool's
// ReadOnly flag and Display string.
package tools

import (
	"context"
	"encoding/json"
)

// Result is the outcome of a tool execution. A non-zero ExitCode is a
// normal result (the command ran but failed), not a Go error; errors are
// reserved for the tool itself failing to run.
type Result struct {
	Output   string
	ExitCode int
}

type Tool interface {
	Name() string
	Description() string
	Schema() json.RawMessage // JSON Schema for the parameters object
	// ReadOnly reports whether the tool can never change system state.
	ReadOnly() bool
	// Display is a short human-readable form of a specific call, shown to
	// the user and fed to the safety gate (for run_command it is the
	// command itself, so command-pattern rules apply).
	Display(args json.RawMessage) string
	Execute(ctx context.Context, args json.RawMessage) (Result, error)
}

// Registry is the ordered set of tools available to a session.
type Registry struct {
	tools map[string]Tool
	order []string
}

func NewRegistry(ts ...Tool) *Registry {
	r := &Registry{tools: make(map[string]Tool, len(ts))}
	for _, t := range ts {
		r.tools[t.Name()] = t
		r.order = append(r.order, t.Name())
	}
	return r
}

func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

func (r *Registry) List() []Tool {
	out := make([]Tool, 0, len(r.order))
	for _, n := range r.order {
		out = append(out, r.tools[n])
	}
	return out
}
