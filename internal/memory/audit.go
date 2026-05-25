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

// AuditRecord is a stored audit row read back for display, including its
// timestamp.
type AuditRecord struct {
	AuditEntry
	CreatedAt string
}

// RecentAudit returns up to n most recent audit rows, newest first.
func (s *Store) RecentAudit(ctx context.Context, n int) ([]AuditRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT source, command, risk, reversible, decision, exit_code, output_excerpt, created_at
FROM audit ORDER BY id DESC LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AuditRecord
	for rows.Next() {
		var r AuditRecord
		var reversible int
		if err := rows.Scan(&r.Source, &r.Command, &r.Risk, &reversible, &r.Decision,
			&r.ExitCode, &r.Output, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.Reversible = reversible != 0
		out = append(out, r)
	}
	return out, rows.Err()
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
