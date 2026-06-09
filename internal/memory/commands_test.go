package memory

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseCommand(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		wantName string
		wantDesc string
		wantBody string
	}{
		{
			name:     "frontmatter",
			raw:      "---\nname: Deploy\ndescription: ship it\n---\nrun the deploy\n",
			wantName: "deploy", // lowercased
			wantDesc: "ship it",
			wantBody: "run the deploy",
		},
		{
			name:     "no frontmatter is all body",
			raw:      "#!/bin/sh\necho hi\n",
			wantName: "",
			wantDesc: "",
			wantBody: "#!/bin/sh\necho hi",
		},
		{
			name:     "crlf line endings",
			raw:      "---\r\nname: x\r\ndescription: y\r\n---\r\nbody\r\n",
			wantName: "x",
			wantDesc: "y",
			wantBody: "body",
		},
		{
			name:     "unterminated fence stays body",
			raw:      "---\nname: x\nno closing fence here",
			wantName: "",
			wantDesc: "",
			wantBody: "---\nname: x\nno closing fence here",
		},
		{
			name:     "desc alias",
			raw:      "---\ndesc: short\n---\nbody",
			wantName: "",
			wantDesc: "short",
			wantBody: "body",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := parseCommand(tt.raw)
			if c.Name != tt.wantName {
				t.Errorf("name = %q, want %q", c.Name, tt.wantName)
			}
			if c.Description != tt.wantDesc {
				t.Errorf("desc = %q, want %q", c.Description, tt.wantDesc)
			}
			if c.Body != tt.wantBody {
				t.Errorf("body = %q, want %q", c.Body, tt.wantBody)
			}
		})
	}
}

func TestLoadCommands(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("deploy.md", "---\nname: deploy\ndescription: ship\n---\ndo the deploy")
	write("bare.md", "echo just a script")         // name from filename
	write("empty.md", "---\nname: skip\n---\n   ") // no body → skipped
	write("notes.txt", "ignored, not .md")

	cmds, err := LoadCommands(dir)
	if err != nil {
		t.Fatalf("LoadCommands: %v", err)
	}
	// bare + deploy, sorted by filename (bare.md, deploy.md); empty.md skipped.
	if len(cmds) != 2 {
		t.Fatalf("want 2 commands, got %d: %+v", len(cmds), cmds)
	}
	if cmds[0].Name != "bare" || cmds[0].Body != "echo just a script" {
		t.Errorf("bare command parsed wrong: %+v", cmds[0])
	}
	if _, ok := FindCommand(cmds, "DEPLOY"); !ok {
		t.Error("FindCommand should match case-insensitively")
	}
	if _, ok := FindCommand(cmds, "nope"); ok {
		t.Error("FindCommand should not match an unknown name")
	}
}

func TestLoadCommandsMissingDir(t *testing.T) {
	cmds, err := LoadCommands(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("missing dir should not error: %v", err)
	}
	if cmds != nil {
		t.Errorf("missing dir should yield no commands, got %+v", cmds)
	}
}
