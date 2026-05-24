// Package model abstracts the LLM backend so providers are swappable.
// M1 implements streaming text chat; the request type already carries a
// Tools field so the M2 tool-calling loop won't need to change this
// interface.
package model

import (
	"context"
	"encoding/json"
)

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool" // a tool-result message fed back to the model
)

// ToolCall is a model's request to invoke a tool. Arguments is the raw
// JSON object the model produced for the tool's parameters.
type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

type Message struct {
	Role    Role
	Content string

	// ToolCalls is set on an assistant message that invoked tools.
	ToolCalls []ToolCall
	// ToolCallID links a RoleTool message back to the ToolCall it answers.
	ToolCallID string
	// Reasoning carries a thinking model's reasoning_content. It must be
	// replayed to the API on the next turn for such models; it stays empty
	// for non-thinking models and is then never sent.
	Reasoning string
}

// Tool describes a callable tool exposed to the model.
type Tool struct {
	Name        string
	Description string
	Schema      json.RawMessage // JSON Schema for the parameters object
}

type ChatRequest struct {
	System   string
	Messages []Message
	Tools    []Tool
}

type EventType int

const (
	EventTextDelta EventType = iota
	EventToolCall
	EventDone
	EventError
	// EventReasoningDelta carries a thinking model's streamed
	// reasoning_content (DeepSeek extension). Text holds the fragment.
	EventReasoningDelta
)

// ChatEvent is one item in a streaming response. Text is set for
// EventTextDelta and EventReasoningDelta; Tool for EventToolCall; Err for
// EventError.
type ChatEvent struct {
	Type EventType
	Text string
	Tool *ToolCall
	Err  error
}

// Provider is a swappable LLM backend.
type Provider interface {
	Name() string
	Model() string
	// StreamChat returns a channel of events. The returned error covers
	// request setup; streaming failures arrive as an EventError on the
	// channel. The channel is closed when the response ends.
	StreamChat(ctx context.Context, req ChatRequest) (<-chan ChatEvent, error)
}

// send delivers an event unless the context is cancelled. It returns
// false when the caller should stop streaming.
func send(ctx context.Context, ch chan<- ChatEvent, ev ChatEvent) bool {
	select {
	case ch <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

// rawOrEmpty returns s as raw JSON, defaulting to an empty object so a
// tool call with no arguments still decodes.
func rawOrEmpty(s string) json.RawMessage {
	if s == "" {
		return json.RawMessage("{}")
	}
	return json.RawMessage(s)
}
