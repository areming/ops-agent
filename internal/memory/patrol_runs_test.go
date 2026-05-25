package memory

import (
	"context"
	"path/filepath"
	"testing"
)

func TestPatrolRunsInsertAndRecent(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	// A fresh store has no runs.
	runs, err := s.RecentPatrolRuns(ctx, 5)
	if err != nil {
		t.Fatalf("RecentPatrolRuns: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("fresh store has %d runs, want 0", len(runs))
	}

	for _, ck := range []string{`["disk"]`, `["disk","load"]`, `["key_services"]`} {
		if _, err := s.InsertPatrolRun(ctx, PatrolRun{
			StartedAt:    "2026-05-25T00:00:00Z",
			FinishedAt:   "2026-05-25T00:00:01Z",
			ChecksJSON:   ck,
			FindingsJSON: `[]`,
		}); err != nil {
			t.Fatalf("InsertPatrolRun: %v", err)
		}
	}

	runs, err = s.RecentPatrolRuns(ctx, 2)
	if err != nil {
		t.Fatalf("RecentPatrolRuns: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("got %d runs, want 2", len(runs))
	}
	// Newest first.
	if runs[0].ChecksJSON != `["key_services"]` || runs[1].ChecksJSON != `["disk","load"]` {
		t.Fatalf("runs not newest-first: %+v", runs)
	}
}
