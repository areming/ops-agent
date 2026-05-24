package memory

import (
	"context"
	"time"
)

const auditExcerptBytes = 2 << 10

// AuditEntry records one state-changing action. Read-only actions are not
// audited.
type AuditEntry struct {
	Source     string // chat | patrol
	Command    string
	Risk       string // low | medium | high
	Reversible bool
	Decision   string // auto | approved | denied
	ExitCode   int
	Output     string
}

func (s *Store) InsertAudit(ctx context.Context, e AuditEntry) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO audit (source, command, risk, reversible, decision, exit_code, output_excerpt, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		e.Source, e.Command, e.Risk, b2i(e.Reversible), e.Decision, e.ExitCode,
		excerpt(e.Output), time.Now().UTC().Format(time.RFC3339))
	return err
}

// CountAudit returns the number of audit rows.
func (s *Store) CountAudit(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM audit`).Scan(&n)
	return n, err
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func excerpt(s string) string {
	if len(s) <= auditExcerptBytes {
		return s
	}
	return s[:auditExcerptBytes]
}
