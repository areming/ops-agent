package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/areming/ops-agent/internal/transport"
)

func TestModelManageText(t *testing.T) {
	var gotCmd, gotArg string
	rt := func(cmd, arg string) (transport.ControlReplyPayload, error) {
		gotCmd, gotArg = cmd, arg
		if cmd == transport.CmdModelList {
			b, _ := json.Marshal(transport.ModelListReply{Profiles: []transport.ModelProfile{
				{ID: "a", Label: "DeepSeek / deepseek-chat", Provider: "deepseek", Active: true},
				{ID: "b", Label: "Anthropic / claude-sonnet-4-6", Provider: "anthropic"},
			}})
			return transport.ControlReplyPayload{Text: string(b)}, nil
		}
		return transport.ControlReplyPayload{Text: "已切换"}, nil
	}

	// No arg → lists both profiles, marking the active one.
	var buf bytes.Buffer
	if err := modelManageText(rt, &buf, ""); err != nil {
		t.Fatalf("list: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "deepseek-chat") || !strings.Contains(out, "claude-sonnet-4-6") {
		t.Errorf("list missing a profile: %q", out)
	}
	if !strings.Contains(out, "* DeepSeek / deepseek-chat") {
		t.Errorf("active profile not marked: %q", out)
	}

	// An arg → switch by name (no list fetch).
	if err := modelManageText(rt, &buf, "claude-sonnet-4-6"); err != nil {
		t.Fatalf("switch: %v", err)
	}
	if gotCmd != transport.CmdModelSwitch || gotArg != "claude-sonnet-4-6" {
		t.Errorf("switch sent cmd=%q arg=%q, want model.switch / claude-sonnet-4-6", gotCmd, gotArg)
	}
}

func TestModelManageTextEmpty(t *testing.T) {
	rt := func(cmd, arg string) (transport.ControlReplyPayload, error) {
		b, _ := json.Marshal(transport.ModelListReply{})
		return transport.ControlReplyPayload{Text: string(b)}, nil
	}
	var buf bytes.Buffer
	if err := modelManageText(rt, &buf, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "还没有模型配置") {
		t.Errorf("empty list should hint at adding one: %q", buf.String())
	}
}
