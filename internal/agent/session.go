package agent

import (
	"context"
	"log"

	"github.com/areming/ops-agent/internal/memory"
	"github.com/areming/ops-agent/internal/model"
)

// baseSystemPrompt is the always-present instruction set. Per-server
// knowledge files are folded in at startup by composeSystemPrompt.
const baseSystemPrompt = "You are opsagent, a lightweight ops assistant that helps manage servers. " +
	"You can run shell commands and read/write files via tools. Be concise and precise. " +
	"When calling a tool that changes the system, set `reversible` and `risk` honestly."

// composeSystemPrompt appends loaded knowledge to the base prompt.
func composeSystemPrompt(knowledge string) string {
	if knowledge == "" {
		return baseSystemPrompt
	}
	return baseSystemPrompt + "\n\n# Server knowledge\n\n" + knowledge
}

// session holds the conversation for one CLI connection. It mirrors every
// message to the store so a later connection can recall the thread.
type session struct {
	store *memory.Store
	depth int
	msgs  []model.Message
}

func newSession(store *memory.Store, depth int) *session {
	return &session{store: store, depth: depth}
}

// hydrate seeds the session with the most recent persisted messages so a
// reopened connection continues the same rolling thread.
func (s *session) hydrate(ctx context.Context) error {
	if s.store == nil || s.depth <= 0 {
		return nil
	}
	msgs, err := s.store.RecentMessages(ctx, s.depth)
	if err != nil {
		return err
	}
	s.msgs = msgs
	return nil
}

func (s *session) addUser(ctx context.Context, text string) {
	s.append(ctx, model.Message{Role: model.RoleUser, Content: text})
}

func (s *session) addAssistant(ctx context.Context, text, reasoning string) {
	s.append(ctx, model.Message{
		Role:      model.RoleAssistant,
		Content:   text,
		Reasoning: reasoning,
	})
}

func (s *session) addAssistantWithCalls(ctx context.Context, text, reasoning string, calls []model.ToolCall) {
	s.append(ctx, model.Message{
		Role:      model.RoleAssistant,
		Content:   text,
		Reasoning: reasoning,
		ToolCalls: calls,
	})
}

func (s *session) addToolResult(ctx context.Context, callID, content string) {
	s.append(ctx, model.Message{
		Role:       model.RoleTool,
		ToolCallID: callID,
		Content:    content,
	})
}

func (s *session) append(ctx context.Context, m model.Message) {
	s.msgs = append(s.msgs, m)
	if s.store == nil {
		return
	}
	if err := s.store.AppendMessage(ctx, m); err != nil {
		log.Printf("persist message: %v", err)
	}
}
