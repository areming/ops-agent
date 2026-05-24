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
)

type Message struct {
	Role    Role
	Content string
}

// Tool is forward-declared for M2; M1 always sends an empty slice.
type Tool struct {
	Name        string
	Description string
	Schema      json.RawMessage
}

type ChatRequest struct {
	System   string
	Messages []Message
	Tools    []Tool
}

type EventType int

const (
	EventTextDelta EventType = iota
	EventDone
	EventError
)

// ChatEvent is one item in a streaming response. Text is set for
// EventTextDelta; Err is set for EventError.
type ChatEvent struct {
	Type EventType
	Text string
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
