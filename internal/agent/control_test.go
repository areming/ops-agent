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
	"github.com/areming/ops-agent/internal/secret"
	"github.com/areming/ops-agent/internal/transport"
)

func clearModelEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"OPSAGENT_PROVIDER", "OPSAGENT_MODEL", "OPSAGENT_BASE_URL", "OPSAGENT_API_KEY"} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
}

func TestModelListAndSwitch(t *testing.T) {
	clearModelEnv(t)
	dir := t.TempDir()
	t.Setenv("OPSAGENT_STATE_DIR", dir)
	t.Setenv("OPSAGENT_API_KEY", "sk-x") // resolveAPIKey returns this; no keystore needed

	if _, err := config.AddProfile(dir, config.Profile{Provider: "deepseek", Model: "deepseek-chat"}); err != nil {
		t.Fatal(err)
	}
	if _, err := config.AddProfile(dir, config.Profile{Provider: "deepseek", Model: "deepseek-reasoner"}); err != nil {
		t.Fatal(err)
	}

	cfg := config.Load() // active = the last added (deepseek-reasoner)
	prov, err := model.New(cfg.Provider, cfg.APIKey, cfg.BaseURL, cfg.Model)
	if err != nil {
		t.Fatal(err)
	}
	srv := &server{cfg: cfg, prov: prov}

	out, err := srv.modelList()
	if err != nil {
		t.Fatalf("modelList: %v", err)
	}
	var lr transport.ModelListReply
	if err := json.Unmarshal([]byte(out), &lr); err != nil {
		t.Fatalf("modelList json: %v (%q)", err, out)
	}
	if len(lr.Profiles) != 2 {
		t.Fatalf("want 2 profiles, got %d", len(lr.Profiles))
	}

	// Switch by model name; the choice persists via config.json.
	if _, err := srv.modelSwitch("deepseek-chat"); err != nil {
		t.Fatalf("modelSwitch: %v", err)
	}
	if got := srv.provider().Model(); got != "deepseek-chat" {
		t.Errorf("provider model = %q, want deepseek-chat", got)
	}
	if got := config.Load().Model; got != "deepseek-chat" {
		t.Errorf("persisted model = %q, want deepseek-chat", got)
	}
}

func TestModelAddSealsKeyAndDeleteGuards(t *testing.T) {
	clearModelEnv(t) // no OPSAGENT_API_KEY: the sealed per-profile key must be used
	dir := t.TempDir()
	t.Setenv("OPSAGENT_STATE_DIR", dir)

	srv := &server{cfg: config.Load()}

	add := func(modelName, key string) {
		b, _ := json.Marshal(transport.ModelAddRequest{Provider: "deepseek", Model: modelName, Key: key})
		if _, err := srv.modelAdd(string(b)); err != nil {
			t.Fatalf("modelAdd %s: %v", modelName, err)
		}
	}
	add("deepseek-chat", "sk-chat") // becomes active, key sealed
	if got := srv.provider().Model(); got != "deepseek-chat" {
		t.Fatalf("after add, provider model = %q", got)
	}
	// The key was sealed under the profile's own ref, readable back.
	profs, _ := config.ListProfiles(dir)
	ks, err := secret.Open(srv.cfg.KeystorePath, srv.cfg.MasterKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok, _ := ks.Get(profs[0].KeyRef); !ok || v != "sk-chat" {
		t.Errorf("profile key not sealed: ok=%v v=%q", ok, v)
	}

	add("deepseek-reasoner", "sk-reason") // a second; now active

	// Deleting the active profile is refused; deleting the other succeeds.
	_, active := config.ListProfiles(dir)
	if _, err := srv.modelDelete(active); err == nil {
		t.Error("deleting the active profile should be refused")
	}
	if _, err := srv.modelDelete("deepseek-chat"); err != nil {
		t.Fatalf("delete non-active: %v", err)
	}
	if profs, _ := config.ListProfiles(dir); len(profs) != 1 {
		t.Fatalf("want 1 profile after delete, got %d", len(profs))
	}
	// Deleting the last remaining profile is refused.
	if _, err := srv.modelDelete("deepseek-reasoner"); err == nil {
		t.Error("deleting the last profile should be refused")
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
