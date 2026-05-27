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

	// yolo auto-approves non-danger write actions for this connection, set
	// via /yolo. Hard danger rules still require confirmation.
	yolo bool
	// approved holds exact command strings the user approved "always" this
	// session (the "a" answer to a confirm prompt).
	approved map[string]bool
}

// approveAlways records cmd so the same command auto-runs for this session.
func (s *session) approveAlways(cmd string) {
	if s.approved == nil {
		s.approved = map[string]bool{}
	}
	s.approved[cmd] = true
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
	s.msgs = sanitizeHistory(msgs)
	return nil
}

// sanitizeHistory trims a recalled message window into a valid tool-call
// transcript, which the model API requires: every assistant tool_call must be
// followed by matching tool results. A raw window can violate this two ways —
// the rolling LIMIT can start mid-exchange, orphaning leading tool results
// whose assistant call was cut out; and an interrupted turn can persist an
// assistant call whose tool results were never written, leaving it dangling
// at the tail. Both make the next request 400, so drop them here.
func sanitizeHistory(msgs []model.Message) []model.Message {
	// Drop leading tool results with no introducing assistant call in window.
	start := 0
	for start < len(msgs) && msgs[start].Role == model.RoleTool {
		start++
	}
	msgs = msgs[start:]

	// Drop any trailing assistant message whose tool_calls were never
	// answered (the answers would follow it, but it is last).
	for len(msgs) > 0 {
		last := msgs[len(msgs)-1]
		if last.Role == model.RoleAssistant && len(last.ToolCalls) > 0 {
			msgs = msgs[:len(msgs)-1]
			continue
		}
		break
	}
	return msgs
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
