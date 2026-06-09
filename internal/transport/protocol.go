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

	// TypeCancel interrupts the turn in progress. The CLI sends it (ESC /
	// Ctrl-C) while the agent is streaming or running tools; the agent cancels
	// the turn's context and closes it with the usual Done frame. It carries no
	// payload and is ignored when no turn is running.
	TypeCancel FrameType = "cancel" // cli->agent

	// TypeToolOutput streams a chunk of a running command's combined
	// stdout/stderr to the CLI for live display, so a long step (e.g. an image
	// build) shows progress instead of a frozen-looking cursor. Display only:
	// the model still receives the full result separately. Payload is a JSON
	// string (the raw output chunk).
	TypeToolOutput FrameType = "tool_output" // agent->cli, display only

	// TypeRunCommand triggers a saved custom command (a `/name` typed at the
	// prompt that is not a built-in). Unlike a control request, it opens a full
	// turn: the agent injects the command's definition as the turn's input and
	// streams the usual deltas / tool activity / confirmations, ending with Done.
	// Payload is a RunCommandPayload.
	TypeRunCommand FrameType = "run_command" // cli->agent
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

// Model management (the /model panel) rides on control frames: the Cmd names
// the action (ModelList/Switch/Add/Delete) and structured data is JSON-encoded
// into the request Arg or reply Text, so no new frame type is needed. The add
// request carries the API key — it travels the same SSH-tunneled socket the
// session already uses and the daemon seals it into its keystore on arrival.
const (
	CmdModelList   = "model.list"
	CmdModelSwitch = "model.switch" // Arg = profile id (or, off-TTY, a name to match)
	CmdModelAdd    = "model.add"    // Arg = JSON ModelAddRequest
	CmdModelDelete = "model.delete" // Arg = profile id

	// CmdCommandList lists the saved custom commands so the client can show them
	// in /help and /commands. The agent replies with a JSON CommandListReply in
	// the control reply Text.
	CmdCommandList = "command.list"
)

// RunCommandPayload names the custom command to run and carries any extra text
// typed after it (e.g. `/deploy staging` → Name="deploy", Args="staging").
type RunCommandPayload struct {
	Name string `json:"name"`
	Args string `json:"args,omitempty"`
}

// CommandInfo is one custom command in a CommandListReply: its trigger name and
// one-line description, for display only.
type CommandInfo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// CommandListReply is the JSON the agent returns (in the control reply Text)
// for CmdCommandList.
type CommandListReply struct {
	Commands []CommandInfo `json:"commands"`
	// Dir is the absolute directory the answering agent reads *.md command
	// files from. It is surfaced to the operator because a local in-process
	// session and the resident daemon resolve different state dirs — a file
	// dropped in the wrong one silently never loads, so the client names the
	// real path instead of a vague "commands 目录".
	Dir string `json:"dir,omitempty"`
}

// ModelProfile is one saved model configuration in a ModelListReply.
type ModelProfile struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	BaseURL  string `json:"base_url,omitempty"`
	Active   bool   `json:"active,omitempty"`
}

// ModelListReply is the JSON the agent returns (in the control reply Text) for
// CmdModelList.
type ModelListReply struct {
	Profiles []ModelProfile `json:"profiles"`
}

// ModelAddRequest is the JSON the client sends (in the control request Arg) for
// CmdModelAdd: a new profile plus its API key to seal.
type ModelAddRequest struct {
	Label    string `json:"label,omitempty"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	BaseURL  string `json:"base_url,omitempty"`
	Key      string `json:"key"`
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
