package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadKnowledgeMissingDir(t *testing.T) {
	got, err := LoadKnowledge(filepath.Join(t.TempDir(), "absent"))
	if err != nil {
		t.Fatalf("LoadKnowledge: %v", err)
	}
	if got != "" {
		t.Errorf("missing dir = %q, want empty", got)
	}
}

func TestLoadKnowledgeConcatenatesSortedMarkdown(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("02-services.md", "nginx runs on port 8080")
	write("01-host.md", "this host is db-primary")
	write("notes.txt", "ignored non-markdown") // must be skipped

	got, err := LoadKnowledge(dir)
	if err != nil {
		t.Fatalf("LoadKnowledge: %v", err)
	}
	if strings.Contains(got, "ignored") {
		t.Errorf("non-markdown file was included: %q", got)
	}
	// Sorted by filename: host (01) before services (02).
	host := strings.Index(got, "db-primary")
	svc := strings.Index(got, "port 8080")
	if host < 0 || svc < 0 || host > svc {
		t.Errorf("ordering wrong: %q", got)
	}
}
