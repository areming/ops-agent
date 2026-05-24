package agent

import "github.com/areming/ops-agent/internal/model"

// systemPrompt is the M1 baseline. Per-server knowledge files are folded
// in at M3.
const systemPrompt = "You are opsagent, a lightweight ops assistant that helps manage servers. " +
	"You can run shell commands and read/write files via tools. Be concise and precise. " +
	"When calling a tool that changes the system, set `reversible` and `risk` honestly."

// session holds the conversation history for one CLI connection.
type session struct {
	msgs []model.Message
}

func (s *session) addUser(text string) {
	s.msgs = append(s.msgs, model.Message{Role: model.RoleUser, Content: text})
}

func (s *session) addAssistant(text string) {
	s.msgs = append(s.msgs, model.Message{Role: model.RoleAssistant, Content: text})
}

func (s *session) addAssistantWithCalls(text string, calls []model.ToolCall) {
	s.msgs = append(s.msgs, model.Message{
		Role:      model.RoleAssistant,
		Content:   text,
		ToolCalls: calls,
	})
}

func (s *session) addToolResult(callID, content string) {
	s.msgs = append(s.msgs, model.Message{
		Role:       model.RoleTool,
		ToolCallID: callID,
		Content:    content,
	})
}
