package memory

import (
	"context"
	"time"
)

// Todo is a self-heal item the agent recorded instead of acting on a
// high-risk or irreversible finding. Patrol (M5) writes these; the CLI
// surfaces them.
type Todo struct {
	ID              int64
	Source          string // patrol | chat
	Severity        string // low | medium | high
	Title           string
	Detail          string
	SuggestedAction string
	Status          string // open | done | dismissed
}

// InsertTodo records a new open todo and returns its id.
func (s *Store) InsertTodo(ctx context.Context, t Todo) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
INSERT INTO todos (source, severity, title, detail, suggested_action, status, created_at)
VALUES (?, ?, ?, ?, ?, 'open', ?)`,
		t.Source, t.Severity, t.Title, t.Detail, t.SuggestedAction,
		time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListOpenTodos returns open todos, newest first.
func (s *Store) ListOpenTodos(ctx context.Context) ([]Todo, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, source, severity, title, detail, suggested_action, status
FROM todos WHERE status = 'open' ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Todo
	for rows.Next() {
		var t Todo
		if err := rows.Scan(&t.ID, &t.Source, &t.Severity, &t.Title, &t.Detail,
			&t.SuggestedAction, &t.Status); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
