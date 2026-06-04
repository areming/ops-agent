package cli

import (
	"errors"
	"io/fs"
	"os"
	"testing"

	"github.com/areming/ops-agent/internal/config"
	"github.com/areming/ops-agent/internal/secret"
)

// localTestEnv points all state at a temp dir and clears the OPSAGENT_* model
// variables so each test starts unconfigured and isolated.
func localTestEnv(t *testing.T) {
	t.Helper()
	t.Setenv("OPSAGENT_STATE_DIR", t.TempDir())
	for _, k := range []string{
		"OPSAGENT_PROVIDER", "OPSAGENT_MODEL", "OPSAGENT_BASE_URL", "OPSAGENT_API_KEY",
		"OPSAGENT_DIAG_PROVIDER", "OPSAGENT_DIAG_MODEL", "OPSAGENT_DIAG_BASE_URL",
	} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
}

func TestConfiguredFalseWhenNoModel(t *testing.T) {
	localTestEnv(t)
	if configured() {
		t.Fatal("configured() = true on a fresh machine, want false")
	}
	// The keystore must not have been touched: no master key created.
	if _, err := os.Stat(config.Load().MasterKeyPath); err == nil {
		t.Error("master key created during a no-model check; keystore was opened too eagerly")
	}
}

func TestConfiguredFalseWhenModelButNoKey(t *testing.T) {
	localTestEnv(t)
	t.Setenv("OPSAGENT_MODEL", "deepseek-chat")
	if configured() {
		t.Fatal("configured() = true with a model but no key, want false")
	}
}

func TestConfiguredTrueWithEnvKey(t *testing.T) {
	localTestEnv(t)
	t.Setenv("OPSAGENT_MODEL", "deepseek-chat")
	t.Setenv("OPSAGENT_API_KEY", "sk-test")
	if !configured() {
		t.Fatal("configured() = false with model + env key, want true")
	}
}

func TestClassifyResident(t *testing.T) {
	// A connection refused / no socket file looks like this to transport.Dial.
	notRunning := &fs.PathError{Op: "dial", Path: "/run/opsagent/agent.sock", Err: errors.New("connect: no such file or directory")}

	tests := []struct {
		name      string
		dialErr   error
		unitThere bool
		want      residentAction
	}{
		{"socket dialable → attach", nil, true, residentAttach},
		{"socket dialable, no unit → attach", nil, false, residentAttach},
		{"permission denied → guide to group/sudo", fs.ErrPermission, true, residentDenied},
		{"unreachable but unit installed → service down", notRunning, true, residentServiceDown},
		{"unreachable and no unit → standalone session", notRunning, false, residentNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyResident(tt.dialErr, tt.unitThere); got != tt.want {
				t.Errorf("classifyResident(%v, %v) = %d, want %d", tt.dialErr, tt.unitThere, got, tt.want)
			}
		})
	}
}

func TestSeedIdempotent(t *testing.T) {
	localTestEnv(t)
	dir := config.Load().StateDir

	// Re-seeding the same provider/model must not duplicate the profile; it
	// reseals the key and keeps it active.
	if err := Seed("deepseek", "deepseek-chat", "https://api.deepseek.com", "sk-1"); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if err := Seed("deepseek", "deepseek-chat", "https://api.deepseek.com", "sk-2"); err != nil {
		t.Fatalf("re-Seed: %v", err)
	}
	profs, _ := config.ListProfiles(dir)
	if len(profs) != 1 {
		t.Fatalf("re-seeding the same model should not duplicate; got %d profiles", len(profs))
	}
	cfg := config.Load()
	ks, err := secret.Open(cfg.KeystorePath, cfg.MasterKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok, _ := ks.Get(profs[0].KeyRef); !ok || v != "sk-2" {
		t.Errorf("re-seed did not reseal the key: %q ok=%v", v, ok)
	}

	// A different model adds a second profile and becomes active.
	if err := Seed("deepseek", "deepseek-reasoner", "https://api.deepseek.com", "sk-3"); err != nil {
		t.Fatalf("Seed new model: %v", err)
	}
	if profs, _ := config.ListProfiles(dir); len(profs) != 2 {
		t.Fatalf("a different model should add a profile; got %d", len(profs))
	}
	if got := config.Load().Model; got != "deepseek-reasoner" {
		t.Errorf("newest seed not active: %q", got)
	}
}

func TestPersistLocalConfigRoundTrip(t *testing.T) {
	localTestEnv(t)

	if err := persistLocalConfig("deepseek", "deepseek-chat", "https://api.deepseek.com", "DeepSeek / deepseek-chat", "sk-secret"); err != nil {
		t.Fatalf("persistLocalConfig: %v", err)
	}

	// config.Load must reflect the saved model selection.
	cfg := config.Load()
	if cfg.Provider != "deepseek" || cfg.Model != "deepseek-chat" || cfg.BaseURL != "https://api.deepseek.com" {
		t.Fatalf("config not persisted: %+v", cfg)
	}
	// With model + sealed key on disk, the machine now reports configured.
	if !configured() {
		t.Fatal("configured() = false after onboarding persistence, want true")
	}
}
