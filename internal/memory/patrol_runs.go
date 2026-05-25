package memory

import "context"

// PatrolRun records one patrol sweep: which checks ran and what they
// found. Checks and findings are stored as opaque JSON so the patrol
// package owns their shape.
type PatrolRun struct {
	ID           int64
	StartedAt    string
	FinishedAt   string
	ChecksJSON   string // JSON array of check names that ran
	FindingsJSON string // JSON array of findings
}

// InsertPatrolRun records a completed patrol sweep and returns its id.
func (s *Store) InsertPatrolRun(ctx context.Context, r PatrolRun) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
INSERT INTO patrol_runs (started_at, finished_at, checks_json, findings_json)
VALUES (?, ?, ?, ?)`,
		r.StartedAt, r.FinishedAt, r.ChecksJSON, r.FindingsJSON)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// RecentPatrolRuns returns up to n most recent runs, newest first.
func (s *Store) RecentPatrolRuns(ctx context.Context, n int) ([]PatrolRun, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, started_at, finished_at, checks_json, findings_json
FROM patrol_runs ORDER BY id DESC LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PatrolRun
	for rows.Next() {
		var r PatrolRun
		if err := rows.Scan(&r.ID, &r.StartedAt, &r.FinishedAt,
			&r.ChecksJSON, &r.FindingsJSON); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
