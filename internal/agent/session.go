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

	// yolo auto-approves non-danger actions for this connection. Interactive
	// sessions enable it by default (see newInteractiveSession); `/yolo off`
	// turns it off for stricter, per-command confirmation. Hard danger rules
	// (rm -rf, mkfs, shutdown, …) always prompt regardless.
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

// newInteractiveSession is the session for a human-driven connection (the
// local REPL or an SSH client). It auto-runs non-danger actions by default so
// routine reads and writes don't each need a click; hard danger rules still
// prompt, and `/yolo off` restores per-command confirmation. Patrol and
// diagnosis build sessions with newSession directly and stay strict.
func newInteractiveSession(store *memory.Store, depth int) *session {
	s := newSession(store, depth)
	s.yolo = true
	return s
}

// sessionBreakMsgs is a synthetic user+assistant pair appended to hydrated
// history. It is never persisted — assigned directly to s.msgs — so the
// model treats the recalled messages as read-only context and waits for a
// new instruction instead of resuming an interrupted prior task.
var sessionBreakMsgs = []model.Message{
	{Role: model.RoleUser, Content: "[新会话] 以上是历史操作记录，请不要继续历史任务，等待新指令。"},
	{Role: model.RoleAssistant, Content: "好的，新会话开始，请告诉我需要做什么。"},
}

// hydrate seeds the session with the most recent persisted messages so the
// model has context of prior work, then injects a session-break marker so
// it does not automatically continue an interrupted task.
func (s *session) hydrate(ctx context.Context) error {
	if s.store == nil || s.depth <= 0 {
		return nil
	}
	msgs, err := s.store.RecentMessages(ctx, s.depth)
	if err != nil {
		return err
	}
	sanitized := sanitizeHistory(msgs)
	if len(sanitized) == 0 {
		return nil
	}
	s.msgs = append(sanitized, sessionBreakMsgs...)
	return nil
}

// sanitizeHistory trims a recalled message window into a valid tool-call
// transcript. The model API requires every assistant tool_call_id to be
// answered by a matching tool message. A raw window can violate this in three
// ways:
//
//  1. Rolling LIMIT starts mid-exchange: leading tool results whose assistant
//     call was cut out → drop them.
//  2. Interrupted turn with zero results persisted: trailing assistant
//     tool_calls with no following tool messages → covered by (3).
//  3. Interrupted turn with partial results: assistant declared N calls but
//     only M < N results were written before the process died. The last
//     message is a tool result (not an assistant), so a tail-only check misses
//     this. The forward scan below catches it.
func sanitizeHistory(msgs []model.Message) []model.Message {
	// Drop leading orphaned tool results.
	start := 0
	for start < len(msgs) && msgs[start].Role == model.RoleTool {
		start++
	}
	msgs = msgs[start:]

	// Walk forward, tracking the last position known to be fully valid.
	// An assistant message with tool_calls opens an exchange; it is complete
	// only when every call_id is answered by an immediately-following tool
	// message. Stop and trim at the first incomplete exchange.
	validEnd := 0
	i := 0
	for i < len(msgs) {
		m := msgs[i]
		if m.Role != model.RoleAssistant || len(m.ToolCalls) == 0 {
			validEnd = i + 1
			i++
			continue
		}
		needed := make(map[string]bool, len(m.ToolCalls))
		for _, tc := range m.ToolCalls {
			needed[tc.ID] = true
		}
		j := i + 1
		for j < len(msgs) && msgs[j].Role == model.RoleTool {
			delete(needed, msgs[j].ToolCallID)
			j++
		}
		if len(needed) > 0 {
			break // incomplete exchange — trim everything from here
		}
		validEnd = j
		i = j
	}
	return msgs[:validEnd]
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
