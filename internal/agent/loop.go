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

// runTurn drives one user turn to completion: stream the model reply,
// run any tool calls (through the safety gate), feed results back, and
// repeat until the model answers with no further tool calls.
func runTurn(ctx context.Context, conn *transport.Conn, prov model.Provider, reg *tools.Registry, store *memory.Store, sess *session) error {
	modelTools := toModelTools(reg)

	for range maxToolRounds {
		ch, err := prov.StreamChat(ctx, model.ChatRequest{
			System:   systemPrompt,
			Messages: sess.msgs,
			Tools:    modelTools,
		})
		if err != nil {
			writeError(conn, err.Error())
			return conn.WriteFrame(transport.Frame{Type: transport.TypeDone})
		}

		var text, reasoning strings.Builder
		var calls []model.ToolCall
		for ev := range ch {
			switch ev.Type {
			case model.EventTextDelta:
				text.WriteString(ev.Text)
				df, ferr := transport.TextFrame(transport.TypeAssistantDelta, ev.Text)
				if ferr != nil {
					return ferr
				}
				if werr := conn.WriteFrame(df); werr != nil {
					return werr
				}
			case model.EventReasoningDelta:
				// Captured for replay to thinking models; not shown yet.
				reasoning.WriteString(ev.Text)
			case model.EventToolCall:
				calls = append(calls, *ev.Tool)
			case model.EventError:
				writeError(conn, ev.Err.Error())
			}
		}

		if len(calls) == 0 {
			sess.addAssistant(text.String(), reasoning.String())
			return conn.WriteFrame(transport.Frame{Type: transport.TypeDone})
		}

		sess.addAssistantWithCalls(text.String(), reasoning.String(), calls)
		for _, call := range calls {
			result := execute(ctx, conn, reg, store, call)
			sess.addToolResult(call.ID, result)
		}
	}

	writeError(conn, fmt.Sprintf("stopped after %d tool rounds", maxToolRounds))
	return conn.WriteFrame(transport.Frame{Type: transport.TypeDone})
}

// execute classifies one tool call, asks the user when needed, runs it,
// audits state changes, and returns the result text to feed back to the
// model.
func execute(ctx context.Context, conn *transport.Conn, reg *tools.Registry, store *memory.Store, call model.ToolCall) string {
	tool, ok := reg.Get(call.Name)
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
		approved, err := confirm(conn, tool.Name(), display, verdict)
		if err != nil {
			return "error: confirmation failed: " + err.Error()
		}
		if !approved {
			audit(ctx, store, display, verdict, "denied", 0, "")
			return "user denied this action; it was not run"
		}
		decision = "approved"
	}

	// Tell the client what is about to run.
	if sf, err := transport.PayloadFrame(transport.TypeToolStart, transport.ToolStartPayload{
		Tool:    tool.Name(),
		Command: display,
	}); err == nil {
		_ = conn.WriteFrame(sf)
	}

	res, err := tool.Execute(ctx, call.Arguments)
	if err != nil {
		if !tool.ReadOnly() {
			audit(ctx, store, display, verdict, decision, -1, err.Error())
		}
		return "tool error: " + err.Error()
	}
	if !tool.ReadOnly() {
		audit(ctx, store, display, verdict, decision, res.ExitCode, res.Output)
	}
	return formatResult(res)
}

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

func audit(ctx context.Context, store *memory.Store, command string, v safety.Verdict, decision string, exitCode int, output string) {
	if store == nil {
		return
	}
	_ = store.InsertAudit(ctx, memory.AuditEntry{
		Source:     "chat",
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
