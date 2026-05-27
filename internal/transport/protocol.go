// Package transport carries the CLI<->agent conversation as a stream of
// length-prefixed JSON Frames. M0 defines only the minimal frame set
// needed to prove the round-trip; richer types (tool calls, confirm
// requests) arrive in later milestones.
package transport

import "encoding/json"

type FrameType string

const (
	TypeUserInput      FrameType = "user_input"
	TypeAssistantDelta FrameType = "assistant_delta"
	TypeDone           FrameType = "done"
	TypeError          FrameType = "error"

	// M2: tool execution and the confirmation handshake.
	TypeToolStart      FrameType = "tool_start"      // agent->cli, display only
	TypeConfirmRequest FrameType = "confirm_request" // agent->cli
	TypeConfirmReply   FrameType = "confirm_reply"   // cli->agent

	// M7: in-conversation slash commands. The CLI sends a control request
	// instead of user text; the agent answers with a single control reply
	// (no Done frame). Used for /models, /logs, /clear.
	TypeControlRequest FrameType = "control_request" // cli->agent
	TypeControlReply   FrameType = "control_reply"   // agent->cli
)

// ControlRequestPayload carries a slash command and its optional argument
// (e.g. Cmd="models", Arg="deepseek-chat").
type ControlRequestPayload struct {
	Cmd string `json:"cmd"`
	Arg string `json:"arg,omitempty"`
}

// ControlReplyPayload is the agent's answer to a control request. Text is
// shown to the user; a non-empty Err reports the command failed.
type ControlReplyPayload struct {
	Text string `json:"text,omitempty"`
	Err  string `json:"err,omitempty"`
}

// ToolStartPayload notifies the client that a tool is about to run, for
// display only.
type ToolStartPayload struct {
	Tool    string `json:"tool"`
	Command string `json:"command,omitempty"`
}

// ConfirmRequestPayload asks the user to approve an action the safety
// gate flagged.
type ConfirmRequestPayload struct {
	Tool    string `json:"tool"`
	Command string `json:"command"`
	Risk    string `json:"risk"`
	Reason  string `json:"reason"`
}

// ConfirmReplyPayload carries the user's decision. Always asks the agent to
// auto-approve this exact command for the rest of the session (the "a" answer).
type ConfirmReplyPayload struct {
	Approved bool `json:"approved"`
	Always   bool `json:"always,omitempty"`
}

// Frame is one message on the wire. Payload is type-specific JSON; for
// the M0 frame types it is a JSON string (or empty for Done).
type Frame struct {
	Type    FrameType       `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// TextFrame builds a frame whose payload is a single JSON string.
func TextFrame(t FrameType, s string) (Frame, error) {
	p, err := json.Marshal(s)
	if err != nil {
		return Frame{}, err
	}
	return Frame{Type: t, Payload: p}, nil
}

// Text decodes a string payload. Empty payload decodes to "".
func (f Frame) Text() (string, error) {
	if len(f.Payload) == 0 {
		return "", nil
	}
	var s string
	err := json.Unmarshal(f.Payload, &s)
	return s, err
}

// PayloadFrame builds a frame whose payload is an arbitrary JSON value.
func PayloadFrame(t FrameType, v any) (Frame, error) {
	p, err := json.Marshal(v)
	if err != nil {
		return Frame{}, err
	}
	return Frame{Type: t, Payload: p}, nil
}

// Decode unmarshals the payload into v.
func (f Frame) Decode(v any) error {
	return json.Unmarshal(f.Payload, v)
}
