package memory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenReadOnlyReadsExistingData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")

	// Populate via a normal read-write open, then close.
	rw, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ctx := context.Background()
	if err := rw.InsertAudit(ctx, AuditEntry{Source: "chat", Command: "uptime", Decision: "auto"}); err != nil {
		t.Fatalf("InsertAudit: %v", err)
	}
	rw.Close()

	// A read-only viewer must read it back without migrating (no write).
	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	defer ro.Close()

	got, err := ro.RecentAudit(ctx, 10)
	if err != nil {
		t.Fatalf("RecentAudit: %v", err)
	}
	if len(got) != 1 || got[0].Command != "uptime" {
		t.Fatalf("read-only audit = %+v, want one 'uptime' row", got)
	}
}

func TestOpenReadOnlyMissingFile(t *testing.T) {
	_, err := OpenReadOnly(filepath.Join(t.TempDir(), "absent.db"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("OpenReadOnly(absent) err = %v, want os.ErrNotExist", err)
	}
}
