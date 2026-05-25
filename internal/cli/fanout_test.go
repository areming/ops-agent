package cli

import (
	"errors"
	"net"
	"testing"

	"github.com/areming/ops-agent/internal/transport"
)

// scriptedAgent plays the agent side of one turn over conn: it reads the
// user input, streams a delta, asks to confirm once, then sends Done. It
// reports back the Approved value it received in the confirm reply.
func scriptedAgent(t *testing.T, conn *transport.Conn, approvedCh chan<- bool) {
	t.Helper()
	if _, err := conn.ReadFrame(); err != nil { // user input
		t.Errorf("agent read user input: %v", err)
		return
	}
	delta, _ := transport.TextFrame(transport.TypeAssistantDelta, "working on it")
	if err := conn.WriteFrame(delta); err != nil {
		t.Errorf("agent write delta: %v", err)
		return
	}
	req, _ := transport.PayloadFrame(transport.TypeConfirmRequest, transport.ConfirmRequestPayload{
		Tool: "run_command", Command: "rm -rf /tmp/x",
	})
	if err := conn.WriteFrame(req); err != nil {
		t.Errorf("agent write confirm req: %v", err)
		return
	}
	reply, err := conn.ReadFrame()
	if err != nil {
		t.Errorf("agent read confirm reply: %v", err)
		return
	}
	var p transport.ConfirmReplyPayload
	_ = reply.Decode(&p)
	approvedCh <- p.Approved
	_ = conn.WriteFrame(transport.Frame{Type: transport.TypeDone})
}

func TestRunOneTurnDeclinesByDefault(t *testing.T) {
	c1, c2 := net.Pipe()
	client, agent := transport.NewConn(c1), transport.NewConn(c2)
	approved := make(chan bool, 1)
	go scriptedAgent(t, agent, approved)

	text, declined, err := runOneTurn(client, "do it", false)
	if err != nil {
		t.Fatalf("runOneTurn: %v", err)
	}
	if text != "working on it" {
		t.Errorf("text = %q, want %q", text, "working on it")
	}
	if declined != 1 {
		t.Errorf("declined = %d, want 1", declined)
	}
	if got := <-approved; got {
		t.Error("confirm reply was Approved=true; default must decline")
	}
}

func TestRunOneTurnApproveAll(t *testing.T) {
	c1, c2 := net.Pipe()
	client, agent := transport.NewConn(c1), transport.NewConn(c2)
	approved := make(chan bool, 1)
	go scriptedAgent(t, agent, approved)

	_, declined, err := runOneTurn(client, "do it", true)
	if err != nil {
		t.Fatalf("runOneTurn: %v", err)
	}
	if declined != 0 {
		t.Errorf("declined = %d, want 0 with approveAll", declined)
	}
	if got := <-approved; !got {
		t.Error("confirm reply was Approved=false; --yes must approve")
	}
}

func TestPrintFanOutFailsWhenHostFails(t *testing.T) {
	if err := printFanOut([]turnResult{
		{host: "a", text: "ok"},
		{host: "b", err: errors.New("boom")},
	}); err == nil {
		t.Error("printFanOut returned nil despite a failed host")
	}
	if err := printFanOut([]turnResult{{host: "a", text: "ok"}}); err != nil {
		t.Errorf("printFanOut errored with no failures: %v", err)
	}
}
