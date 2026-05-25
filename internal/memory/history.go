package memory

import (
	"context"
	"encoding/json"
	"time"

	"github.com/areming/ops-agent/internal/model"
)

// AppendMessage persists one conversation message. Tool calls are stored as
// JSON so an assistant turn that invoked tools can be replayed verbatim.
func (s *Store) AppendMessage(ctx context.Context, m model.Message) error {
	var toolCalls string
	if len(m.ToolCalls) > 0 {
		b, err := json.Marshal(m.ToolCalls)
		if err != nil {
			return err
		}
		toolCalls = string(b)
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO messages (role, content, tool_calls, tool_call_id, reasoning, created_at)
VALUES (?, ?, ?, ?, ?, ?)`,
		string(m.Role), m.Content, toolCalls, m.ToolCallID, m.Reasoning,
		time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

// RecentMessages returns up to n most recent messages in chronological
// order, so a reopened session resumes the same rolling thread.
func (s *Store) RecentMessages(ctx context.Context, n int) ([]model.Message, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT role, content, tool_calls, tool_call_id, reasoning
FROM messages ORDER BY id DESC LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.Message
	for rows.Next() {
		var role, content, toolCalls, toolCallID, reasoning string
		if err := rows.Scan(&role, &content, &toolCalls, &toolCallID, &reasoning); err != nil {
			return nil, err
		}
		m := model.Message{
			Role:       model.Role(role),
			Content:    content,
			ToolCallID: toolCallID,
			Reasoning:  reasoning,
		}
		if toolCalls != "" {
			if err := json.Unmarshal([]byte(toolCalls), &m.ToolCalls); err != nil {
				return nil, err
			}
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	reverse(out)
	return out, nil
}

func reverse(m []model.Message) {
	for i, j := 0, len(m)-1; i < j; i, j = i+1, j-1 {
		m[i], m[j] = m[j], m[i]
	}
}
