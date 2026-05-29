package agent

import (
	"net"
	"testing"

	"github.com/areming/ops-agent/internal/safety"
	"github.com/areming/ops-agent/internal/transport"
)

// connPair returns the agent and client ends of an in-memory frame connection.
func connPair(t *testing.T) (agentConn, clientConn *transport.Conn) {
	t.Helper()
	a, c := net.Pipe()
	t.Cleanup(func() { a.Close(); c.Close() })
	return transport.NewConn(a), transport.NewConn(c)
}

// answerConfirm reads exactly one confirm request on conn and sends reply,
// returning a channel that closes once it has answered.
func answerConfirm(t *testing.T, conn *transport.Conn, reply transport.ConfirmReplyPayload) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		f, err := conn.ReadFrame()
		if err != nil {
			t.Errorf("read confirm request: %v", err)
			return
		}
		if f.Type != transport.TypeConfirmRequest {
			t.Errorf("expected confirm request, got %s", f.Type)
			return
		}
		rf, err := transport.PayloadFrame(transport.TypeConfirmReply, reply)
		if err != nil {
			t.Errorf("build reply: %v", err)
			return
		}
		if err := conn.WriteFrame(rf); err != nil {
			t.Errorf("write reply: %v", err)
		}
	}()
	return done
}

// A successful return without any goroutine answering the wire proves the path
// short-circuited before prompting (an unanswered prompt would block on write).
func TestConfirmYoloSkipsNonDanger(t *testing.T) {
	agentConn, _ := connPair(t)
	sess := newSession(nil, 0)
	sess.yolo = true
	ia := &connInteraction{conn: agentConn, sess: sess}

	ok, err := ia.confirm("shell", "systemctl restart nginx", safety.Verdict{Decision: safety.Confirm})
	if err != nil {
		t.Fatalf("confirm err: %v", err)
	}
	if !ok {
		t.Fatal("yolo did not auto-approve a non-danger write")
	}
}

func TestConfirmYoloStillPromptsDanger(t *testing.T) {
	agentConn, clientConn := connPair(t)
	sess := newSession(nil, 0)
	sess.yolo = true
	ia := &connInteraction{conn: agentConn, sess: sess}

	done := answerConfirm(t, clientConn, transport.ConfirmReplyPayload{Approved: false})
	ok, err := ia.confirm("shell", "rm -rf /data", safety.Verdict{Decision: safety.Confirm, Danger: true})
	if err != nil {
		t.Fatalf("confirm err: %v", err)
	}
	if ok {
		t.Fatal("danger command was auto-approved under yolo")
	}
	<-done
}

// An interactive session auto-runs non-danger actions out of the box (no
// /yolo needed). A successful return without anyone answering the wire proves
// it short-circuited — an unanswered prompt would block on write.
func TestInteractiveSessionAutoApprovesNonDanger(t *testing.T) {
	agentConn, _ := connPair(t)
	sess := newInteractiveSession(nil, 0)
	ia := &connInteraction{conn: agentConn, sess: sess}

	ok, err := ia.confirm("shell", "systemctl restart nginx", safety.Verdict{Decision: safety.Confirm})
	if err != nil {
		t.Fatalf("confirm err: %v", err)
	}
	if !ok {
		t.Fatal("interactive session did not auto-approve a non-danger write by default")
	}
}

func TestInteractiveSessionStillPromptsDanger(t *testing.T) {
	agentConn, clientConn := connPair(t)
	sess := newInteractiveSession(nil, 0)
	ia := &connInteraction{conn: agentConn, sess: sess}

	done := answerConfirm(t, clientConn, transport.ConfirmReplyPayload{Approved: false})
	ok, err := ia.confirm("shell", "rm -rf /data", safety.Verdict{Decision: safety.Confirm, Danger: true})
	if err != nil {
		t.Fatalf("confirm err: %v", err)
	}
	if ok {
		t.Fatal("danger command was auto-approved in an interactive session")
	}
	<-done
}

func TestConfirmAlwaysCachesForSession(t *testing.T) {
	agentConn, clientConn := connPair(t)
	sess := newSession(nil, 0)
	ia := &connInteraction{conn: agentConn, sess: sess}

	done := answerConfirm(t, clientConn, transport.ConfirmReplyPayload{Approved: true, Always: true})
	ok, err := ia.confirm("shell", "systemctl restart nginx", safety.Verdict{Decision: safety.Confirm})
	if err != nil || !ok {
		t.Fatalf("first confirm: ok=%v err=%v", ok, err)
	}
	<-done

	// The second call must be served from the session cache without prompting;
	// nothing is reading the client end now, so a write would block forever.
	ok, err = ia.confirm("shell", "systemctl restart nginx", safety.Verdict{Decision: safety.Confirm})
	if err != nil || !ok {
		t.Fatalf("cached confirm: ok=%v err=%v", ok, err)
	}
}

func TestConfirmAlwaysNotCachedForDanger(t *testing.T) {
	agentConn, clientConn := connPair(t)
	sess := newSession(nil, 0)
	ia := &connInteraction{conn: agentConn, sess: sess}

	done := answerConfirm(t, clientConn, transport.ConfirmReplyPayload{Approved: true, Always: true})
	ok, err := ia.confirm("shell", "rm -rf /data", safety.Verdict{Decision: safety.Confirm, Danger: true})
	if err != nil || !ok {
		t.Fatalf("confirm: ok=%v err=%v", ok, err)
	}
	<-done

	if sess.approved["rm -rf /data"] {
		t.Fatal("danger command was cached for always-approve")
	}
}
