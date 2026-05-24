package agent

import "github.com/areming/ops-agent/internal/model"

// systemPrompt is the M1 baseline. Per-server knowledge files are folded
// in at M3.
const systemPrompt = "You are opsagent, a lightweight ops assistant that helps manage servers. Be concise and precise."

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
