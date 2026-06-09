package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/areming/ops-agent/internal/transport"
)

func TestPrintCommands(t *testing.T) {
	rt := func(cmd, arg string) (transport.ControlReplyPayload, error) {
		if cmd != transport.CmdCommandList {
			t.Errorf("unexpected control cmd %q", cmd)
		}
		b, _ := json.Marshal(transport.CommandListReply{
			Dir: "/var/lib/opsagent/commands",
			Commands: []transport.CommandInfo{
				{Name: "deploy", Description: "部署最新构建"},
				{Name: "bare"},
			},
		})
		return transport.ControlReplyPayload{Text: string(b)}, nil
	}

	var buf bytes.Buffer
	if err := printCommands(rt, &buf, false); err != nil {
		t.Fatalf("printCommands: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "/deploy") || !strings.Contains(out, "部署最新构建") {
		t.Errorf("listing missing a described command: %q", out)
	}
	if !strings.Contains(out, "/bare") {
		t.Errorf("listing missing a description-less command: %q", out)
	}
	// The listing names the real directory so the operator knows where commands
	// live and where to add more.
	if !strings.Contains(out, "/var/lib/opsagent/commands") {
		t.Errorf("listing should name the commands directory: %q", out)
	}
}

func TestPrintCommandsEmpty(t *testing.T) {
	rt := func(cmd, arg string) (transport.ControlReplyPayload, error) {
		b, _ := json.Marshal(transport.CommandListReply{Dir: "/root/.config/opsagent/commands"})
		return transport.ControlReplyPayload{Text: string(b)}, nil
	}

	// Not quiet: an empty list hints at how to add one, naming the real dir.
	var buf bytes.Buffer
	if err := printCommands(rt, &buf, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "还没有自定义命令") {
		t.Errorf("empty list should hint at adding one: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "/root/.config/opsagent/commands") {
		t.Errorf("empty hint should name the commands directory: %q", buf.String())
	}

	// Quiet (the /help tail): an empty list prints nothing.
	buf.Reset()
	if err := printCommands(rt, &buf, true); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Errorf("quiet empty list should print nothing, got %q", buf.String())
	}
}
