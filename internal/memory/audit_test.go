package memory

import (
	"context"
	"path/filepath"
	"testing"
)

func TestInsertAndCountAudit(t *testing.T) {
	db := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(db)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	err = s.InsertAudit(ctx, AuditEntry{
		Source:     "chat",
		Command:    "systemctl restart nginx",
		Risk:       "medium",
		Reversible: true,
		Decision:   "approved",
		ExitCode:   0,
		Output:     "ok",
	})
	if err != nil {
		t.Fatalf("InsertAudit: %v", err)
	}

	var n int
	var cmd, decision string
	row := s.db.QueryRowContext(ctx, `SELECT count(*), command, decision FROM audit`)
	if err := row.Scan(&n, &cmd, &decision); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if n != 1 {
		t.Errorf("audit rows = %d, want 1", n)
	}
	if cmd != "systemctl restart nginx" || decision != "approved" {
		t.Errorf("got command=%q decision=%q", cmd, decision)
	}
}
