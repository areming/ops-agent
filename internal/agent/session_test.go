package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/areming/ops-agent/internal/memory"
	"github.com/areming/ops-agent/internal/model"
)

// assertWellFormed fails if msgs is not a valid tool-call transcript: every
// assistant tool_call must be answered by a following tool message, and no
// tool message may be orphaned (lack a preceding assistant call). These are
// exactly the constraints the OpenAI/DeepSeek API enforces on input.
func assertWellFormed(t *testing.T, msgs []model.Message) {
	t.Helper()
	open := map[string]bool{} // tool_call ids awaiting a result
	for i, m := range msgs {
		switch m.Role {
		case model.RoleAssistant:
			for _, c := range m.ToolCalls {
				open[c.ID] = true
			}
		case model.RoleTool:
			if !open[m.ToolCallID] {
				t.Fatalf("msgs[%d]: orphan tool result for id %q (no preceding assistant call)", i, m.ToolCallID)
			}
			delete(open, m.ToolCallID)
		}
	}
	if len(open) > 0 {
		t.Fatalf("dangling assistant tool_calls never answered: %v", open)
	}
}

// TestHydrateDropsDanglingToolCalls reproduces the live 400 from DeepSeek:
// a prior turn persisted an assistant message with tool_calls but was
// interrupted before its tool result was written, leaving a dangling call in
// the DB. Hydrating it and sending a new turn produced a request the API
// rejected. Hydrate must trim the dangling call.
func TestHydrateDropsDanglingToolCalls(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	// A complete prior exchange, then an interrupted one (no tool result).
	store.AppendMessage(ctx, model.Message{Role: model.RoleUser, Content: "restart nginx"})
	store.AppendMessage(ctx, model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "c1", Name: "run_command", Arguments: json.RawMessage(`{}`)}}})
	store.AppendMessage(ctx, model.Message{Role: model.RoleTool, ToolCallID: "c1", Content: "done"})
	store.AppendMessage(ctx, model.Message{Role: model.RoleAssistant, Content: "and check disk", ToolCalls: []model.ToolCall{{ID: "c2", Name: "run_command", Arguments: json.RawMessage(`{}`)}}})

	sess := newSession(store, 50)
	if err := sess.hydrate(ctx); err != nil {
		t.Fatalf("hydrate: %v", err)
	}

	assertWellFormed(t, sess.msgs)

	// The dangling c2 call must be gone; the complete c1 exchange must remain.
	if last := sess.msgs[len(sess.msgs)-1]; last.Role == model.RoleAssistant && len(last.ToolCalls) > 0 {
		t.Errorf("history still ends with a dangling tool_calls message: %+v", last)
	}
}

// TestHydrateDropsLeadingOrphanToolResults covers the truncation case: the
// rolling window (RecentMessages LIMIT n) can start in the middle of a tool
// exchange, leaving a leading tool result whose introducing assistant call
// was cut out of the window.
func TestHydrateDropsLeadingOrphanToolResults(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	// Window depth 2 keeps only the last two rows: [tool result, user].
	store.AppendMessage(ctx, model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "c1", Name: "run_command", Arguments: json.RawMessage(`{}`)}}})
	store.AppendMessage(ctx, model.Message{Role: model.RoleTool, ToolCallID: "c1", Content: "done"})
	store.AppendMessage(ctx, model.Message{Role: model.RoleUser, Content: "thanks"})

	sess := newSession(store, 2)
	if err := sess.hydrate(ctx); err != nil {
		t.Fatalf("hydrate: %v", err)
	}

	assertWellFormed(t, sess.msgs)
}

// TestHydrateDropsPartialToolResults reproduces the 400 caused by a crash
// mid-turn: the assistant declared N tool_calls but only M < N results were
// written before the process died. The last persisted message is a tool result
// (not an assistant-with-calls), so the old trailing-drop loop missed it.
func TestHydrateDropsPartialToolResults(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	store.AppendMessage(ctx, model.Message{Role: model.RoleUser, Content: "deploy blog"})
	store.AppendMessage(ctx, model.Message{
		Role: model.RoleAssistant,
		ToolCalls: []model.ToolCall{
			{ID: "c1", Name: "shell", Arguments: json.RawMessage(`{}`)},
			{ID: "c2", Name: "shell", Arguments: json.RawMessage(`{}`)},
			{ID: "c3", Name: "shell", Arguments: json.RawMessage(`{}`)},
		},
	})
	store.AppendMessage(ctx, model.Message{Role: model.RoleTool, ToolCallID: "c1", Content: "done"})
	store.AppendMessage(ctx, model.Message{Role: model.RoleTool, ToolCallID: "c2", Content: "done"})
	// c3 result never written — simulates crash between execute calls.

	sess := newSession(store, 50)
	if err := sess.hydrate(ctx); err != nil {
		t.Fatalf("hydrate: %v", err)
	}
	assertWellFormed(t, sess.msgs)
}

// TestHydrateInjectsSessionBreak verifies that non-empty history gets a
// synthetic session-break pair appended so the model does not auto-continue
// a prior task when the user starts a fresh conversation.
func TestHydrateInjectsSessionBreak(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	store.AppendMessage(ctx, model.Message{Role: model.RoleUser, Content: "check nginx"})
	store.AppendMessage(ctx, model.Message{Role: model.RoleAssistant, Content: "done"})

	sess := newSession(store, 50)
	if err := sess.hydrate(ctx); err != nil {
		t.Fatalf("hydrate: %v", err)
	}

	n := len(sess.msgs)
	if n < 2 {
		t.Fatalf("expected at least 2 msgs after hydrate, got %d", n)
	}
	if got := sess.msgs[n-2].Content; got != sessionBreakMsgs[0].Content {
		t.Errorf("session break user msg: got %q, want %q", got, sessionBreakMsgs[0].Content)
	}
	if got := sess.msgs[n-1].Content; got != sessionBreakMsgs[1].Content {
		t.Errorf("session break asst msg: got %q, want %q", got, sessionBreakMsgs[1].Content)
	}
	assertWellFormed(t, sess.msgs)
}

// TestHydrateEmptyHistoryNoBreak verifies that an empty DB produces no msgs
// at all — no synthetic break is injected for a brand-new install.
func TestHydrateEmptyHistoryNoBreak(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	sess := newSession(store, 50)
	if err := sess.hydrate(ctx); err != nil {
		t.Fatalf("hydrate: %v", err)
	}
	if len(sess.msgs) != 0 {
		t.Errorf("expected empty msgs for empty history, got %d", len(sess.msgs))
	}
}

func openTestStore(t *testing.T) *memory.Store {
	t.Helper()
	store, err := memory.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}
