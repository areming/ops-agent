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

func TestProfileRoundTrip(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	t.Setenv("OPSAGENT_STATE_DIR", dir)

	p1, err := AddProfile(dir, Profile{Provider: "deepseek", Model: "deepseek-chat", BaseURL: "https://api.deepseek.com"})
	if err != nil {
		t.Fatalf("AddProfile: %v", err)
	}
	if p1.ID == "" || p1.KeyRef == "" {
		t.Fatalf("AddProfile did not assign id/key_ref: %+v", p1)
	}
	p2, err := AddProfile(dir, Profile{Provider: "anthropic", Model: "claude-sonnet-4-6", BaseURL: "https://api.anthropic.com"})
	if err != nil {
		t.Fatal(err)
	}
	if p2.KeyRef == p1.KeyRef {
		t.Errorf("each profile needs its own key ref, got %q twice", p2.KeyRef)
	}

	// Adding makes the new one active.
	if got := Load(); got.Provider != "anthropic" || got.Model != "claude-sonnet-4-6" || got.KeyRef != p2.KeyRef {
		t.Fatalf("newest profile not active: %+v", got)
	}

	// Switch back to the first.
	if err := SetActive(dir, p1.ID); err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	if got := Load(); got.Model != "deepseek-chat" || got.KeyRef != p1.KeyRef {
		t.Fatalf("SetActive did not take: %+v", got)
	}
	if ps, active := ListProfiles(dir); len(ps) != 2 || active != p1.ID {
		t.Fatalf("ListProfiles = %+v active=%q", ps, active)
	}

	// Deleting the active profile reassigns active to the remaining one.
	removed, err := DeleteProfile(dir, p1.ID)
	if err != nil || removed.ID != p1.ID {
		t.Fatalf("DeleteProfile: %v removed=%+v", err, removed)
	}
	if got := Load(); got.Model != "claude-sonnet-4-6" {
		t.Fatalf("after deleting active, it was not reassigned: %+v", got)
	}

	// The persisted file must never contain an API key value.
	b, err := os.ReadFile(filepath.Join(dir, configFileName))
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	if containsKey(string(b)) {
		t.Errorf("saved config unexpectedly contains an api key: %s", b)
	}
}

func TestMigrateLegacyConfig(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	t.Setenv("OPSAGENT_STATE_DIR", dir)
	writeConfig(t, dir, `{"provider":"deepseek","model":"deepseek-chat","base_url":"https://x"}`)

	cfg := Load()
	if cfg.Provider != "deepseek" || cfg.Model != "deepseek-chat" || cfg.BaseURL != "https://x" {
		t.Fatalf("legacy config not loaded: %+v", cfg)
	}
	if cfg.KeyRef != LegacyKeyName {
		t.Errorf("migrated profile must keep the legacy key ref, got %q", cfg.KeyRef)
	}
	ps, active := ListProfiles(dir)
	if len(ps) != 1 || ps[0].ID != "default" || active != "default" {
		t.Fatalf("legacy did not migrate to one default profile: %+v active=%q", ps, active)
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
