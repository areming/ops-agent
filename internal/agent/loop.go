package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/areming/ops-agent/internal/memory"
	"github.com/areming/ops-agent/internal/model"
	"github.com/areming/ops-agent/internal/safety"
	"github.com/areming/ops-agent/internal/tools"
	"github.com/areming/ops-agent/internal/transport"
)

// maxToolRounds caps the model<->tool cycle so a misbehaving model can't
// loop forever.
const maxToolRounds = 25

// engine is the reusable model<->tool loop: it streams a model reply, runs
// tool calls through the safety gate, and feeds results back until the
// model answers with none. It is shared by the chat path (over a CLI
// connection) and patrol's diagnosis path (connectionless); the differences
// live behind the interaction.
type engine struct {
	reg   *tools.Registry
	store *memory.Store
}

// interaction is how a running turn talks to its initiator. The chat path
// streams to a CLI connection and asks a human to confirm; patrol discards
// the chatter and refuses (recording the decline) any action that needs
// confirmation, since no human is attached.
type interaction interface {
	// source labels audit rows for this turn ("chat" | "patrol").
	source() string
	// onDelta receives streamed assistant text. A returned error (e.g. a
	// broken connection) aborts the turn.
	onDelta(text string) error
	// onToolStart announces a tool about to run; best-effort.
	onToolStart(tool, command string)
	// onError surfaces a recoverable error; best-effort.
	onError(msg string)
	// confirm reports whether a Confirm-verdict action may run.
	confirm(tool, command string, v safety.Verdict) (bool, error)
	// declineRun records a Confirm-verdict action that will not run and
	// returns the text fed back to the model in its place.
	declineRun(ctx context.Context, command string, v safety.Verdict) string
}

// runTurn drives one turn to completion against the given provider and
// system prompt. It returns an error only when the interaction can no
// longer be reached (e.g. the connection dropped); model and tool failures
// are surfaced through the interaction and fed back to the model.
func (e *engine) runTurn(ctx context.Context, prov model.Provider, system string, ia interaction, sess *session) error {
	modelTools := toModelTools(e.reg)

	for range maxToolRounds {
		ch, err := prov.StreamChat(ctx, model.ChatRequest{
			System:   system,
			Messages: sess.msgs,
			Tools:    modelTools,
		})
		if err != nil {
			ia.onError(err.Error())
			return nil
		}

		var text, reasoning strings.Builder
		var calls []model.ToolCall
		for ev := range ch {
			switch ev.Type {
			case model.EventTextDelta:
				text.WriteString(ev.Text)
				if derr := ia.onDelta(ev.Text); derr != nil {
					return derr
				}
			case model.EventReasoningDelta:
				// Captured for replay to thinking models; not shown yet.
				reasoning.WriteString(ev.Text)
			case model.EventToolCall:
				calls = append(calls, *ev.Tool)
			case model.EventError:
				ia.onError(ev.Err.Error())
			}
		}

		if len(calls) == 0 {
			sess.addAssistant(ctx, text.String(), reasoning.String())
			return nil
		}

		sess.addAssistantWithCalls(ctx, text.String(), reasoning.String(), calls)
		for _, call := range calls {
			result := e.execute(ctx, ia, call)
			sess.addToolResult(ctx, call.ID, result)
		}
	}

	ia.onError(fmt.Sprintf("stopped after %d tool rounds", maxToolRounds))
	return nil
}

// execute classifies one tool call, consults the interaction when the gate
// asks for confirmation, runs it, audits state changes, and returns the
// result text to feed back to the model.
func (e *engine) execute(ctx context.Context, ia interaction, call model.ToolCall) string {
	tool, ok := e.reg.Get(call.Name)
	if !ok {
		return "error: unknown tool " + call.Name
	}
	display := tool.Display(call.Arguments)

	var eval safety.SelfEval
	_ = json.Unmarshal(call.Arguments, &eval)
	verdict := safety.Classify(safety.Action{
		Display:  display,
		ReadOnly: tool.ReadOnly(),
		Eval:     eval,
	})

	decision := "auto"
	if verdict.Decision == safety.Confirm {
		approved, err := ia.confirm(tool.Name(), display, verdict)
		if err != nil {
			return "error: confirmation failed: " + err.Error()
		}
		if !approved {
			return ia.declineRun(ctx, display, verdict)
		}
		decision = "approved"
	}

	ia.onToolStart(tool.Name(), display)

	res, err := tool.Execute(ctx, call.Arguments)
	if err != nil {
		if !tool.ReadOnly() {
			audit(ctx, e.store, ia.source(), display, verdict, decision, -1, err.Error())
		}
		return "tool error: " + err.Error()
	}
	if !tool.ReadOnly() {
		audit(ctx, e.store, ia.source(), display, verdict, decision, res.ExitCode, res.Output)
	}
	return formatResult(res)
}

// confirm runs the request/reply handshake over a CLI connection.
func confirm(conn *transport.Conn, tool, command string, v safety.Verdict) (bool, error) {
	req, err := transport.PayloadFrame(transport.TypeConfirmRequest, transport.ConfirmRequestPayload{
		Tool:    tool,
		Command: command,
		Risk:    v.Risk,
		Reason:  v.Reason,
	})
	if err != nil {
		return false, err
	}
	if err := conn.WriteFrame(req); err != nil {
		return false, err
	}
	f, err := conn.ReadFrame()
	if err != nil {
		return false, err
	}
	if f.Type != transport.TypeConfirmReply {
		return false, fmt.Errorf("expected confirm reply, got %s", f.Type)
	}
	var reply transport.ConfirmReplyPayload
	if err := f.Decode(&reply); err != nil {
		return false, err
	}
	return reply.Approved, nil
}

func audit(ctx context.Context, store *memory.Store, source, command string, v safety.Verdict, decision string, exitCode int, output string) {
	if store == nil {
		return
	}
	_ = store.InsertAudit(ctx, memory.AuditEntry{
		Source:     source,
		Command:    command,
		Risk:       v.Risk,
		Reversible: v.Reversible,
		Decision:   decision,
		ExitCode:   exitCode,
		Output:     output,
	})
}

func formatResult(res tools.Result) string {
	out := res.Output
	if out == "" {
		out = "(no output)"
	}
	if res.ExitCode != 0 {
		return fmt.Sprintf("(exit %d)\n%s", res.ExitCode, out)
	}
	return out
}

func toModelTools(reg *tools.Registry) []model.Tool {
	list := reg.List()
	out := make([]model.Tool, 0, len(list))
	for _, t := range list {
		out = append(out, model.Tool{
			Name:        t.Name(),
			Description: t.Description(),
			Schema:      t.Schema(),
		})
	}
	return out
}
