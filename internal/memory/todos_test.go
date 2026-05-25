package memory

import (
	"context"
	"path/filepath"
	"testing"
)

func TestTodosInsertAndListOpen(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	// A fresh store has no todos.
	open, err := s.ListOpenTodos(ctx)
	if err != nil {
		t.Fatalf("ListOpenTodos: %v", err)
	}
	if len(open) != 0 {
		t.Fatalf("fresh store has %d todos, want 0", len(open))
	}

	id, err := s.InsertTodo(ctx, Todo{
		Source:          "patrol",
		Severity:        "high",
		Title:           "disk almost full on /",
		Detail:          "92% used",
		SuggestedAction: "clean /var/log",
	})
	if err != nil {
		t.Fatalf("InsertTodo: %v", err)
	}

	open, err = s.ListOpenTodos(ctx)
	if err != nil {
		t.Fatalf("ListOpenTodos: %v", err)
	}
	if len(open) != 1 || open[0].ID != id || open[0].Title != "disk almost full on /" {
		t.Fatalf("unexpected open todos: %+v", open)
	}
	if open[0].Status != "open" {
		t.Errorf("status = %q, want open", open[0].Status)
	}
}

func TestRecentAuditNewestFirst(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "state.db"))
	defer s.Close()
	ctx := context.Background()

	for _, cmd := range []string{"first", "second", "third"} {
		if err := s.InsertAudit(ctx, AuditEntry{Source: "chat", Command: cmd, Decision: "auto"}); err != nil {
			t.Fatalf("InsertAudit: %v", err)
		}
	}

	got, err := s.RecentAudit(ctx, 2)
	if err != nil {
		t.Fatalf("RecentAudit: %v", err)
	}
	if len(got) != 2 || got[0].Command != "third" || got[1].Command != "second" {
		t.Fatalf("RecentAudit window wrong: %+v", got)
	}
	if got[0].CreatedAt == "" {
		t.Error("RecentAudit row missing CreatedAt")
	}
}
