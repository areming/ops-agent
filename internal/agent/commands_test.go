package agent

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/areming/ops-agent/internal/config"
	"github.com/areming/ops-agent/internal/memory"
	"github.com/areming/ops-agent/internal/model"
	"github.com/areming/ops-agent/internal/tools"
	"github.com/areming/ops-agent/internal/transport"
)

func TestCommandList(t *testing.T) {
	dir := writeCommand(t, "deploy.md", "---\nname: deploy\ndescription: ship it\n---\ndo the deploy")
	srv := &server{cfg: config.Config{CommandsDir: dir}}

	out, err := srv.commandList()
	if err != nil {
		t.Fatalf("commandList: %v", err)
	}
	var lr transport.CommandListReply
	if err := json.Unmarshal([]byte(out), &lr); err != nil {
		t.Fatalf("commandList json: %v (%q)", err, out)
	}
	if len(lr.Commands) != 1 || lr.Commands[0].Name != "deploy" || lr.Commands[0].Description != "ship it" {
		t.Fatalf("unexpected command list: %+v", lr.Commands)
	}
}

func TestBuildCommandPrompt(t *testing.T) {
	cmd := memory.Command{Name: "deploy", Description: "ship", Body: "restart the web service"}

	got := buildCommandPrompt(cmd, "")
	if !strings.Contains(got, "/deploy") {
		t.Errorf("prompt missing command name: %q", got)
	}
	if !strings.Contains(got, "restart the web service") {
		t.Errorf("prompt missing body: %q", got)
	}
	if strings.Contains(got, "附加参数") {
		t.Errorf("no args given but args section present: %q", got)
	}

	withArgs := buildCommandPrompt(cmd, "  staging  ")
	if !strings.Contains(withArgs, "[附加参数] staging") {
		t.Errorf("args not included/trimmed: %q", withArgs)
	}
}

// writeCommand drops a command file into a temp commands dir and returns the
// dir.
func writeCommand(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// driveRunCommand runs runCommandTurn over a net.Pipe and returns the streamed
// assistant text plus whether a Done frame closed the turn.
func driveRunCommand(t *testing.T, srv *server, sess *session, p transport.RunCommandPayload) (text string, gotDone, gotErr bool) {
	t.Helper()
	c1, c2 := net.Pipe()
	t.Cleanup(func() { c1.Close(); c2.Close() })
	agentSide := transport.NewConn(c1)
	clientSide := transport.NewConn(c2)

	frames := make(chan transport.Frame)
	go func() {
		defer close(frames)
		for {
			f, err := agentSide.ReadFrame()
			if err != nil {
				return
			}
			frames <- f
		}
	}()

	errc := make(chan error, 1)
	go func() { errc <- srv.runCommandTurn(context.Background(), agentSide, sess, frames, p) }()

	var b strings.Builder
loop:
	for {
		f, ferr := clientSide.ReadFrame()
		if ferr != nil {
			t.Fatalf("client read: %v", ferr)
		}
		switch f.Type {
		case transport.TypeAssistantDelta:
			s, _ := f.Text()
			b.WriteString(s)
		case transport.TypeError:
			gotErr = true
		case transport.TypeDone:
			gotDone = true
			break loop
		}
	}
	if err := <-errc; err != nil {
		t.Fatalf("runCommandTurn: %v", err)
	}
	return b.String(), gotDone, gotErr
}

func TestRunCommandTurnUnknown(t *testing.T) {
	dir := t.TempDir() // empty: no commands
	srv := &server{cfg: config.Config{CommandsDir: dir}}
	sess := newSession(nil, 0)

	_, gotDone, gotErr := driveRunCommand(t, srv, sess, transport.RunCommandPayload{Name: "nope"})
	if !gotErr {
		t.Error("unknown command should emit an error frame")
	}
	if !gotDone {
		t.Error("unknown command should still close the turn with Done")
	}
	if len(sess.msgs) != 0 {
		t.Errorf("unknown command should not touch the session, got %d msgs", len(sess.msgs))
	}
}

func TestRunCommandTurnRuns(t *testing.T) {
	store, err := memory.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	dir := writeCommand(t, "hello.md", "---\nname: hello\ndescription: greet\n---\nsay hello to the operator")
	prov := &fakeProvider{rounds: [][]model.ChatEvent{
		{{Type: model.EventTextDelta, Text: "done"}},
	}}
	srv := &server{
		cfg:          config.Config{CommandsDir: dir},
		prov:         prov,
		eng:          &engine{reg: tools.NewRegistry(), store: store},
		store:        store,
		systemPrompt: baseSystemPrompt,
	}
	sess := newSession(store, 0)

	text, gotDone, _ := driveRunCommand(t, srv, sess, transport.RunCommandPayload{Name: "hello", Args: "warmly"})
	if !gotDone {
		t.Fatal("turn did not close with Done")
	}
	if text != "done" {
		t.Errorf("streamed text = %q, want %q", text, "done")
	}
	// The command's definition was injected as the user turn.
	if len(sess.msgs) == 0 || sess.msgs[0].Role != model.RoleUser {
		t.Fatalf("expected an injected user message, got %+v", sess.msgs)
	}
	injected := sess.msgs[0].Content
	if !strings.Contains(injected, "say hello to the operator") || !strings.Contains(injected, "warmly") {
		t.Errorf("injected prompt missing body or args: %q", injected)
	}
}
