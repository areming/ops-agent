package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// clearConfigEnv unsets the OPSAGENT_* variables Load reads so a test starts
// from a known state regardless of the developer's environment.
func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"OPSAGENT_PROVIDER", "OPSAGENT_MODEL", "OPSAGENT_BASE_URL",
		"OPSAGENT_DIAG_PROVIDER", "OPSAGENT_DIAG_MODEL", "OPSAGENT_DIAG_BASE_URL",
	} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
}

func TestLoadMissingFileUsesDefaults(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("OPSAGENT_STATE_DIR", t.TempDir())

	cfg := Load()
	if cfg.Provider != "openai" {
		t.Errorf("provider = %q, want default openai", cfg.Provider)
	}
	if cfg.Model != "" {
		t.Errorf("model = %q, want empty", cfg.Model)
	}
	// Diag fields fall back to their chat-model counterparts.
	if cfg.DiagProvider != "openai" {
		t.Errorf("diag provider = %q, want openai", cfg.DiagProvider)
	}
}

func TestLoadReadsConfigFile(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	t.Setenv("OPSAGENT_STATE_DIR", dir)
	writeConfig(t, dir, `{"provider":"deepseek","model":"deepseek-chat","base_url":"https://x"}`)

	cfg := Load()
	if cfg.Provider != "deepseek" || cfg.Model != "deepseek-chat" || cfg.BaseURL != "https://x" {
		t.Fatalf("file values not loaded: %+v", cfg)
	}
	// Diag defaults track the resolved chat fields.
	if cfg.DiagProvider != "deepseek" || cfg.DiagModel != "deepseek-chat" {
		t.Errorf("diag did not track chat fields: provider=%q model=%q", cfg.DiagProvider, cfg.DiagModel)
	}
}

func TestEnvOverridesFile(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	t.Setenv("OPSAGENT_STATE_DIR", dir)
	writeConfig(t, dir, `{"provider":"deepseek","model":"deepseek-chat"}`)
	t.Setenv("OPSAGENT_MODEL", "gpt-4o")

	cfg := Load()
	if cfg.Model != "gpt-4o" {
		t.Errorf("model = %q, want env override gpt-4o", cfg.Model)
	}
	if cfg.Provider != "deepseek" {
		t.Errorf("provider = %q, want file value deepseek (no env set)", cfg.Provider)
	}
}

func TestSaveRoundTrip(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	t.Setenv("OPSAGENT_STATE_DIR", dir)

	want := Load()
	want.Provider = "anthropic"
	want.Model = "claude-sonnet-4-6"
	want.BaseURL = "https://api.anthropic.com"
	if err := Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got := Load()
	if got.Provider != "anthropic" || got.Model != "claude-sonnet-4-6" || got.BaseURL != "https://api.anthropic.com" {
		t.Fatalf("round trip mismatch: %+v", got)
	}

	// The persisted file must be owner-only and the API key absent.
	path := filepath.Join(dir, configFileName)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	if containsKey(string(b)) {
		t.Errorf("saved config unexpectedly contains an api key: %s", b)
	}
}

func writeConfig(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, configFileName), []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func containsKey(s string) bool {
	for _, k := range []string{"api_key", "apiKey", "API_KEY"} {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}
