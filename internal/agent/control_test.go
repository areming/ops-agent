package agent

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/areming/ops-agent/internal/config"
	"github.com/areming/ops-agent/internal/memory"
	"github.com/areming/ops-agent/internal/model"
	"github.com/areming/ops-agent/internal/transport"
)

func clearModelEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"OPSAGENT_PROVIDER", "OPSAGENT_MODEL", "OPSAGENT_BASE_URL", "OPSAGENT_API_KEY"} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
}

func TestControlModelsList(t *testing.T) {
	prov, err := model.New("deepseek", "sk-x", "", "deepseek-chat")
	if err != nil {
		t.Fatal(err)
	}
	srv := &server{cfg: config.Config{Provider: "deepseek"}, prov: prov}

	out, err := srv.controlModels("")
	if err != nil {
		t.Fatalf("controlModels: %v", err)
	}
	if !strings.Contains(out, "deepseek-reasoner") {
		t.Errorf("list missing a known model: %q", out)
	}
	if !strings.Contains(out, "* deepseek-chat") {
		t.Errorf("current model not marked: %q", out)
	}
}

func TestControlModelsSwitchPersists(t *testing.T) {
	clearModelEnv(t)
	dir := t.TempDir()
	t.Setenv("OPSAGENT_STATE_DIR", dir)

	cfg := config.Config{Provider: "deepseek", Model: "deepseek-chat", APIKey: "sk-x", StateDir: dir}
	prov, err := model.New(cfg.Provider, cfg.APIKey, cfg.BaseURL, cfg.Model)
	if err != nil {
		t.Fatal(err)
	}
	srv := &server{cfg: cfg, prov: prov}

	if _, err := srv.controlModels("deepseek-reasoner"); err != nil {
		t.Fatalf("switch: %v", err)
	}
	if got := srv.provider().Model(); got != "deepseek-reasoner" {
		t.Errorf("provider model = %q, want deepseek-reasoner", got)
	}
	// The choice must survive a reload via config.json.
	if got := config.Load().Model; got != "deepseek-reasoner" {
		t.Errorf("persisted model = %q, want deepseek-reasoner", got)
	}
}

func TestControlLogs(t *testing.T) {
	dir := t.TempDir()
	store, err := memory.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	srv := &server{store: store}
	ctx := context.Background()

	out, err := srv.controlLogs(ctx, "")
	if err != nil {
		t.Fatalf("controlLogs empty: %v", err)
	}
	if !strings.Contains(out, "暂无") {
		t.Errorf("empty store output = %q, want a no-entries message", out)
	}

	if err := store.InsertAudit(ctx, memory.AuditEntry{
		Source: "chat", Command: "ls -la", Risk: "low", Decision: "auto",
	}); err != nil {
		t.Fatal(err)
	}
	out, err = srv.controlLogs(ctx, "")
	if err != nil {
		t.Fatalf("controlLogs: %v", err)
	}
	if !strings.Contains(out, "ls -la") {
		t.Errorf("logs output missing the audited command: %q", out)
	}
}

func TestHandleControlClear(t *testing.T) {
	sess := &session{msgs: []model.Message{{Role: model.RoleUser, Content: "hi"}}}
	srv := &server{}

	client, srvEnd := net.Pipe()
	defer client.Close()
	defer srvEnd.Close()

	reqFrame, err := transport.PayloadFrame(transport.TypeControlRequest, transport.ControlRequestPayload{Cmd: "clear"})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_ = srv.handleControl(context.Background(), transport.NewConn(srvEnd), sess, reqFrame)
	}()

	f, err := transport.NewConn(client).ReadFrame()
	if err != nil {
		t.Fatalf("read reply: %v", err)
	}
	if f.Type != transport.TypeControlReply {
		t.Fatalf("reply type = %s, want control_reply", f.Type)
	}
	if len(sess.msgs) != 0 {
		t.Errorf("session not cleared: %d messages remain", len(sess.msgs))
	}
}
