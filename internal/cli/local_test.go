package cli

import (
	"os"
	"testing"

	"github.com/areming/ops-agent/internal/config"
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

func TestPersistLocalConfigRoundTrip(t *testing.T) {
	localTestEnv(t)

	if err := persistLocalConfig("deepseek", "deepseek-chat", "https://api.deepseek.com", "sk-secret"); err != nil {
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
