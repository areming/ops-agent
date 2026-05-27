package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileMetadata(t *testing.T) {
	if !(ReadFile{}).ReadOnly() {
		t.Error("ReadFile.ReadOnly() = false, want true")
	}
	if (WriteFile{}).ReadOnly() {
		t.Error("WriteFile.ReadOnly() = true, want false")
	}
	if !json.Valid(ReadFile{}.Schema()) {
		t.Error("ReadFile.Schema() invalid JSON")
	}
	if !json.Valid(WriteFile{}.Schema()) {
		t.Error("WriteFile.Schema() invalid JSON")
	}
	if got := (ReadFile{}).Display(json.RawMessage(`{"path":"/etc/hosts"}`)); got != "read /etc/hosts" {
		t.Errorf("ReadFile.Display() = %q", got)
	}
	if got := (WriteFile{}).Display(json.RawMessage(`{"path":"/tmp/x"}`)); got != "write /tmp/x" {
		t.Errorf("WriteFile.Display() = %q", got)
	}
}

func TestWriteThenRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.txt")
	content := "hello world"

	wargs, _ := json.Marshal(writeArgs{Path: path, Content: content})
	wres, err := WriteFile{}.Execute(context.Background(), wargs)
	if err != nil {
		t.Fatalf("WriteFile.Execute() error = %v", err)
	}
	if wres.ExitCode != 0 {
		t.Errorf("WriteFile ExitCode = %d, want 0 (output: %q)", wres.ExitCode, wres.Output)
	}
	if want := fmt.Sprintf("wrote %d bytes", len(content)); !strings.Contains(wres.Output, want) {
		t.Errorf("WriteFile Output = %q, want it to contain %q", wres.Output, want)
	}

	rargs, _ := json.Marshal(pathArgs{Path: path})
	rres, err := ReadFile{}.Execute(context.Background(), rargs)
	if err != nil {
		t.Fatalf("ReadFile.Execute() error = %v", err)
	}
	if rres.Output != content {
		t.Errorf("ReadFile Output = %q, want %q", rres.Output, content)
	}
}

func TestReadFileMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist")
	args, _ := json.Marshal(pathArgs{Path: path})

	// A missing file is a normal failed result (ExitCode 1), not a Go error.
	res, err := ReadFile{}.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("ReadFile.Execute() error = %v, want nil", err)
	}
	if res.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", res.ExitCode)
	}
	if res.Output == "" {
		t.Error("Output is empty, want the underlying error message")
	}
}

func TestWriteFileBadPath(t *testing.T) {
	// Writing into a nonexistent directory fails at the OS level and is
	// reported as a failed result, not a Go error.
	path := filepath.Join(t.TempDir(), "no-such-dir", "file.txt")
	args, _ := json.Marshal(writeArgs{Path: path, Content: "x"})

	res, err := WriteFile{}.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("WriteFile.Execute() error = %v, want nil", err)
	}
	if res.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", res.ExitCode)
	}
}

func TestReadFileTruncates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.txt")
	content := strings.Repeat("a", maxFileBytes+100)

	wargs, _ := json.Marshal(writeArgs{Path: path, Content: content})
	if _, err := (WriteFile{}).Execute(context.Background(), wargs); err != nil {
		t.Fatalf("setup write error = %v", err)
	}

	rargs, _ := json.Marshal(pathArgs{Path: path})
	res, err := ReadFile{}.Execute(context.Background(), rargs)
	if err != nil {
		t.Fatalf("ReadFile.Execute() error = %v", err)
	}
	if !strings.HasSuffix(res.Output, "(truncated)") {
		t.Errorf("Output not truncated: tail = %q", res.Output[len(res.Output)-20:])
	}
}

func TestFileExecuteBadJSON(t *testing.T) {
	if _, err := (ReadFile{}).Execute(context.Background(), json.RawMessage(`{bad`)); err == nil {
		t.Error("ReadFile.Execute() bad JSON = nil error, want error")
	}
	if _, err := (WriteFile{}).Execute(context.Background(), json.RawMessage(`{bad`)); err == nil {
		t.Error("WriteFile.Execute() bad JSON = nil error, want error")
	}
}
