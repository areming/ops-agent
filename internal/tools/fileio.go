package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
)

const maxFileBytes = 64 << 10

type pathArgs struct {
	Path string `json:"path"`
}

// ReadFile returns the contents of a file. It is read-only, so the
// safety gate auto-allows it.
type ReadFile struct{}

func (ReadFile) Name() string        { return "read_file" }
func (ReadFile) Description() string { return "Read and return the contents of a file on the server." }
func (ReadFile) ReadOnly() bool      { return true }

func (ReadFile) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Absolute path to read."}},"required":["path"]}`)
}

func (ReadFile) Display(args json.RawMessage) string {
	var a pathArgs
	_ = json.Unmarshal(args, &a)
	return "read " + a.Path
}

func (ReadFile) Execute(_ context.Context, args json.RawMessage) (Result, error) {
	var a pathArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return Result{}, err
	}
	b, err := os.ReadFile(a.Path)
	if err != nil {
		return Result{Output: err.Error(), ExitCode: 1}, nil
	}
	return Result{Output: truncate(string(b), maxFileBytes)}, nil
}

type writeArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// WriteFile creates or overwrites a file. It is a write operation, so the
// safety gate requires confirmation.
type WriteFile struct{}

func (WriteFile) Name() string { return "write_file" }
func (WriteFile) Description() string {
	return "Create or overwrite a file with the given content. Fill `reversible` and `risk` honestly."
}
func (WriteFile) ReadOnly() bool { return false }

func (WriteFile) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "Absolute path to write."},
    "content": {"type": "string", "description": "Full new file content."},
    "reversible": {"type": "boolean", "description": "False if this overwrites important data irretrievably."},
    "risk": {"type": "string", "enum": ["low", "medium", "high"]}
  },
  "required": ["path", "content"]
}`)
}

func (WriteFile) Display(args json.RawMessage) string {
	var a writeArgs
	_ = json.Unmarshal(args, &a)
	return "write " + a.Path
}

func (WriteFile) Execute(_ context.Context, args json.RawMessage) (Result, error) {
	var a writeArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(a.Path, []byte(a.Content), 0o644); err != nil {
		return Result{Output: err.Error(), ExitCode: 1}, nil
	}
	return Result{Output: fmt.Sprintf("wrote %d bytes to %s", len(a.Content), a.Path)}, nil
}
