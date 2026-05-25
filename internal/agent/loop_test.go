package agent

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/areming/ops-agent/internal/memory"
	"github.com/areming/ops-agent/internal/model"
	"github.com/areming/ops-agent/internal/tools"
	"github.com/areming/ops-agent/internal/transport"
)

// fakeProvider returns a scripted set of events per StreamChat call.
type fakeProvider struct {
	rounds [][]model.ChatEvent
	call   int
}

func (f *fakeProvider) Name() string  { return "fake" }
func (f *fakeProvider) Model() string { return "fake" }
func (f *fakeProvider) StreamChat(_ context.Context, _ model.ChatRequest) (<-chan model.ChatEvent, error) {
	evs := f.rounds[f.call]
	f.call++
	ch := make(chan model.ChatEvent)
	go func() {
		defer close(ch)
		for _, e := range evs {
			ch <- e
		}
	}()
	return ch, nil
}

// stubTool records whether it ran; it never touches the real system.
type stubTool struct {
	executed *bool
}

func (stubTool) Name() string                   { return "do_thing" }
func (stubTool) Description() string            { return "stub" }
func (stubTool) Schema() json.RawMessage        { return json.RawMessage(`{"type":"object"}`) }
func (stubTool) ReadOnly() bool                 { return false }
func (stubTool) Display(json.RawMessage) string { return "do a dangerous thing" }
func (s stubTool) Execute(context.Context, json.RawMessage) (tools.Result, error) {
	*s.executed = true
	return tools.Result{Output: "ok"}, nil
}

// runScenario wires runTurn against a fake provider and drives the client
// side of the pipe, approving or denying the one confirmation it expects.
func runScenario(t *testing.T, approve bool) (executed bool, finalText string, auditRows int) {
	t.Helper()

	store, err := memory.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	prov := &fakeProvider{rounds: [][]model.ChatEvent{
		{{Type: model.EventToolCall, Tool: &model.ToolCall{ID: "c1", Name: "do_thing", Arguments: json.RawMessage(`{}`)}}},
		{{Type: model.EventTextDelta, Text: "all set"}},
	}}
	reg := tools.NewRegistry(stubTool{executed: &executed})

	c1, c2 := net.Pipe()
	agentSide := transport.NewConn(c1)
	clientSide := transport.NewConn(c2)

	srv := &server{prov: prov, reg: reg, store: store, systemPrompt: baseSystemPrompt}
	sess := newSession(store, 0)
	sess.addUser(context.Background(), "do it")

	errc := make(chan error, 1)
	go func() { errc <- srv.runTurn(context.Background(), agentSide, sess) }()

	var text strings.Builder
loop:
	for {
		f, ferr := clientSide.ReadFrame()
		if ferr != nil {
			t.Fatalf("client read: %v", ferr)
		}
		switch f.Type {
		case transport.TypeAssistantDelta:
			s, _ := f.Text()
			text.WriteString(s)
		case transport.TypeConfirmRequest:
			reply, _ := transport.PayloadFrame(transport.TypeConfirmReply, transport.ConfirmReplyPayload{Approved: approve})
			if werr := clientSide.WriteFrame(reply); werr != nil {
				t.Fatalf("client write: %v", werr)
			}
		case transport.TypeDone:
			break loop
		}
	}
	if err := <-errc; err != nil {
		t.Fatalf("runTurn: %v", err)
	}

	rows, err := store.CountAudit(context.Background())
	if err != nil {
		t.Fatalf("count audit: %v", err)
	}
	return executed, text.String(), rows
}

func TestRunTurnApprove(t *testing.T) {
	executed, text, rows := runScenario(t, true)
	if !executed {
		t.Error("tool was not executed after approval")
	}
	if text != "all set" {
		t.Errorf("final text = %q, want %q", text, "all set")
	}
	if rows != 1 {
		t.Errorf("audit rows = %d, want 1", rows)
	}
}

func TestRunTurnDeny(t *testing.T) {
	executed, text, rows := runScenario(t, false)
	if executed {
		t.Error("tool ran despite denial")
	}
	if text != "all set" {
		t.Errorf("final text = %q, want %q", text, "all set")
	}
	if rows != 1 { // denial is still audited
		t.Errorf("audit rows = %d, want 1", rows)
	}
}
