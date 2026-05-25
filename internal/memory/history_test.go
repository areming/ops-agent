package memory

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/areming/ops-agent/internal/model"
)

func TestAppendAndRecentMessages(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	msgs := []model.Message{
		{Role: model.RoleUser, Content: "restart nginx"},
		{Role: model.RoleAssistant, Reasoning: "think", ToolCalls: []model.ToolCall{
			{ID: "c1", Name: "run_command", Arguments: json.RawMessage(`{"cmd":"systemctl restart nginx"}`)},
		}},
		{Role: model.RoleTool, ToolCallID: "c1", Content: "ok"},
		{Role: model.RoleAssistant, Content: "done"},
	}
	for _, m := range msgs {
		if err := s.AppendMessage(ctx, m); err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}
	}

	got, err := s.RecentMessages(ctx, 50)
	if err != nil {
		t.Fatalf("RecentMessages: %v", err)
	}
	if len(got) != len(msgs) {
		t.Fatalf("got %d messages, want %d", len(got), len(msgs))
	}
	// Chronological order preserved.
	if got[0].Content != "restart nginx" || got[3].Content != "done" {
		t.Errorf("order wrong: %q ... %q", got[0].Content, got[3].Content)
	}
	// Tool call replay survives the round-trip.
	if len(got[1].ToolCalls) != 1 || got[1].ToolCalls[0].Name != "run_command" {
		t.Errorf("tool call not restored: %+v", got[1].ToolCalls)
	}
	if got[1].Reasoning != "think" {
		t.Errorf("reasoning not restored: %q", got[1].Reasoning)
	}
	if got[2].ToolCallID != "c1" {
		t.Errorf("tool_call_id not restored: %q", got[2].ToolCallID)
	}
}

func TestRecentMessagesLimitKeepsNewest(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "state.db"))
	defer s.Close()
	ctx := context.Background()

	for _, c := range []string{"one", "two", "three"} {
		_ = s.AppendMessage(ctx, model.Message{Role: model.RoleUser, Content: c})
	}

	got, err := s.RecentMessages(ctx, 2)
	if err != nil {
		t.Fatalf("RecentMessages: %v", err)
	}
	if len(got) != 2 || got[0].Content != "two" || got[1].Content != "three" {
		t.Fatalf("limit kept wrong window: %v", contents(got))
	}
}

func contents(ms []model.Message) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Content
	}
	return out
}
