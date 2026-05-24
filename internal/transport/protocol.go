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
)

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
