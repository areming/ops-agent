// Package config loads agent settings. M1 reads only environment
// variables to stay dependency-free; a TOML file arrives in a later
// milestone when there is more to configure.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Provider string // openai | deepseek | anthropic
	Model    string
	APIKey   string // optional plaintext override; empty means read from the keystore
	KeyRef   string // keystore entry holding the active profile's API key
	BaseURL  string // optional override for OpenAI-compatible/Anthropic
	DBPath   string // SQLite state/audit database

	// Diagnosis model used by patrol to investigate findings. Each field
	// falls back to its chat-model counterpart when its OPSAGENT_DIAG_*
	// variable is unset, so an unconfigured install diagnoses with the main
	// model and shares its API key.
	DiagProvider string
	DiagModel    string
	DiagBaseURL  string

	// StateDir holds the agent's at-rest state: secret keystore, master
	// key, and knowledge files. Per-file paths derive from it.
	StateDir      string
	KeystorePath  string
	MasterKeyPath string
	KnowledgeDir  string
	HistoryDepth  int // messages reloaded into a new session from history

	Patrol PatrolConfig
}

// PatrolConfig controls the background patrol loop. Until TOML config
// arrives (M6) these come from OPSAGENT_PATROL_* environment variables.
type PatrolConfig struct {
	Enabled  bool
	Interval time.Duration
	Checks   []string // subset of: disk, load, key_services
	Services []string // units patrol watches and may auto-restart
	DiskPct  int      // flag a mount when used% is at or above this
	LoadPer  float64  // flag when 1-min load / CPU count is at or above this
}

// Profile is one saved model configuration the user can switch between. The
// API key is never stored in it — KeyRef names the keystore entry that holds
// the key, so config.json carries no secret.
type Profile struct {
	ID       string
	Label    string
	Provider string
	Model    string
	BaseURL  string
	KeyRef   string
}

// profileEntry is the on-disk form of a Profile.
type profileEntry struct {
	ID       string `json:"id"`
	Label    string `json:"label,omitempty"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	BaseURL  string `json:"base_url,omitempty"`
	KeyRef   string `json:"key_ref,omitempty"`
}

func (e profileEntry) toProfile() Profile {
	return Profile{ID: e.ID, Label: e.Label, Provider: e.Provider, Model: e.Model, BaseURL: e.BaseURL, KeyRef: e.KeyRef}
}

// fileConfig is the persistable config at StateDir/config.json: the saved
// model profiles, which one is active, and the diagnosis-model overrides.
// The API key lives in the keystore and paths are derived, so neither is
// stored here.
type fileConfig struct {
	Active string         `json:"active,omitempty"`
	Models []profileEntry `json:"models,omitempty"`

	DiagProvider string `json:"diag_provider,omitempty"`
	DiagModel    string `json:"diag_model,omitempty"`
	DiagBaseURL  string `json:"diag_base_url,omitempty"`

	// Legacy pre-profile fields, read only to migrate an old config.json into
	// a single profile (see migrated). Never written back.
	LegacyProvider string `json:"provider,omitempty"`
	LegacyModel    string `json:"model,omitempty"`
	LegacyBaseURL  string `json:"base_url,omitempty"`
}

// configFileName is the on-disk config under StateDir.
const configFileName = "config.json"

// LegacyKeyName is the keystore entry the pre-profile install used for its one
// API key. Migrated profiles keep pointing at it so an upgrade needs no
// re-entry of the key.
const LegacyKeyName = "api_key"

// migrated returns fc with its profile list populated: a pre-profile config
// (flat provider/model/base_url, key under LegacyKeyName) becomes a single
// "default" profile. The legacy fields are cleared so a re-save drops them.
func (fc fileConfig) migrated() fileConfig {
	if len(fc.Models) == 0 && fc.LegacyProvider != "" {
		fc.Models = []profileEntry{{
			ID:       "default",
			Label:    profileLabel(fc.LegacyProvider, fc.LegacyModel),
			Provider: fc.LegacyProvider,
			Model:    fc.LegacyModel,
			BaseURL:  fc.LegacyBaseURL,
			KeyRef:   LegacyKeyName,
		}}
		fc.Active = "default"
	}
	fc.LegacyProvider, fc.LegacyModel, fc.LegacyBaseURL = "", "", ""
	return fc
}

// activeEntry returns the active profile, falling back to the first one if the
// active id is unset or dangling. ok is false when there are no profiles.
func (fc fileConfig) activeEntry() (profileEntry, bool) {
	for _, e := range fc.Models {
		if e.ID == fc.Active {
			return e, true
		}
	}
	if len(fc.Models) > 0 {
		return fc.Models[0], true
	}
	return profileEntry{}, false
}

// Load resolves settings with precedence env > config.json > built-in
// default, so a value set in-session persists while OPSAGENT_* env vars (set
// by the systemd unit or for dev) still win and never break an existing
// install.
func Load() Config {
	stateDir := getenv("OPSAGENT_STATE_DIR", defaultStateDir())
	fc := loadFile(filepath.Join(stateDir, configFileName)).migrated()
	active, _ := fc.activeEntry()

	keyRef := active.KeyRef
	if keyRef == "" {
		keyRef = LegacyKeyName
	}
	provider := pick("OPSAGENT_PROVIDER", active.Provider, "openai")
	model := pick("OPSAGENT_MODEL", active.Model, "")
	baseURL := pick("OPSAGENT_BASE_URL", active.BaseURL, "")
	return Config{
		Provider:      provider,
		Model:         model,
		APIKey:        os.Getenv("OPSAGENT_API_KEY"),
		KeyRef:        keyRef,
		BaseURL:       baseURL,
		DiagProvider:  pick("OPSAGENT_DIAG_PROVIDER", fc.DiagProvider, provider),
		DiagModel:     pick("OPSAGENT_DIAG_MODEL", fc.DiagModel, model),
		DiagBaseURL:   pick("OPSAGENT_DIAG_BASE_URL", fc.DiagBaseURL, baseURL),
		DBPath:        getenv("OPSAGENT_DB", filepath.Join(stateDir, "state.db")),
		StateDir:      stateDir,
		KeystorePath:  filepath.Join(stateDir, "keystore.json"),
		MasterKeyPath: filepath.Join(stateDir, "master.key"),
		KnowledgeDir:  getenv("OPSAGENT_KNOWLEDGE_DIR", filepath.Join(stateDir, "knowledge")),
		HistoryDepth:  getenvInt("OPSAGENT_HISTORY", 50),
		Patrol:        loadPatrol(),
	}
}

// ListProfiles returns the saved model profiles and the active profile's id
// (migrating a pre-profile config.json on the fly).
func ListProfiles(stateDir string) ([]Profile, string) {
	fc := loadFileConfig(stateDir)
	ps := make([]Profile, 0, len(fc.Models))
	for _, e := range fc.Models {
		ps = append(ps, e.toProfile())
	}
	return ps, fc.Active
}

// ActiveProfile returns the active profile, ok=false when none is configured.
func ActiveProfile(stateDir string) (Profile, bool) {
	e, ok := loadFileConfig(stateDir).activeEntry()
	return e.toProfile(), ok
}

// AddProfile appends p (assigning an id and key_ref when empty, and a label
// when empty), makes it active, and persists. It returns the stored profile so
// the caller knows the KeyRef under which to seal the API key.
func AddProfile(stateDir string, p Profile) (Profile, error) {
	fc := loadFileConfig(stateDir)
	taken := map[string]bool{}
	for _, e := range fc.Models {
		taken[e.ID] = true
	}
	if p.ID == "" {
		p.ID = makeID(firstNonEmpty(p.Model, p.Provider), taken)
	}
	if p.Label == "" {
		p.Label = profileLabel(p.Provider, p.Model)
	}
	if p.KeyRef == "" {
		p.KeyRef = "model." + p.ID + ".key"
	}
	fc.Models = append(fc.Models, profileEntry{
		ID: p.ID, Label: p.Label, Provider: p.Provider,
		Model: p.Model, BaseURL: p.BaseURL, KeyRef: p.KeyRef,
	})
	fc.Active = p.ID
	if err := saveFileConfig(stateDir, fc); err != nil {
		return Profile{}, err
	}
	return p, nil
}

// SetActive marks id active and persists. It errors if id is unknown.
func SetActive(stateDir, id string) error {
	fc := loadFileConfig(stateDir)
	for _, e := range fc.Models {
		if e.ID == id {
			fc.Active = id
			return saveFileConfig(stateDir, fc)
		}
	}
	return fmt.Errorf("no such model profile %q", id)
}

// DeleteProfile removes id and persists; if it was active, the first remaining
// profile becomes active. It returns the removed profile (so the caller can
// delete its key) and errors if id is unknown.
func DeleteProfile(stateDir, id string) (Profile, error) {
	fc := loadFileConfig(stateDir)
	idx := -1
	for i, e := range fc.Models {
		if e.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return Profile{}, fmt.Errorf("no such model profile %q", id)
	}
	removed := fc.Models[idx].toProfile()
	fc.Models = append(fc.Models[:idx], fc.Models[idx+1:]...)
	if fc.Active == id {
		fc.Active = ""
		if len(fc.Models) > 0 {
			fc.Active = fc.Models[0].ID
		}
	}
	if err := saveFileConfig(stateDir, fc); err != nil {
		return Profile{}, err
	}
	return removed, nil
}

// loadFileConfig reads and migrates the on-disk config under stateDir.
func loadFileConfig(stateDir string) fileConfig {
	return loadFile(filepath.Join(stateDir, configFileName)).migrated()
}

// saveFileConfig writes fc atomically (temp + rename) at 0600 — it names the
// providers an install talks to but never the API key.
func saveFileConfig(stateDir string, fc fileConfig) error {
	b, err := json.MarshalIndent(fc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(stateDir, 0o750); err != nil {
		return err
	}
	path := filepath.Join(stateDir, configFileName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// profileLabel is the default display label for a profile: "provider / model".
func profileLabel(provider, model string) string {
	if model == "" {
		return provider
	}
	return provider + " / " + model
}

// makeID derives a stable, readable id from base (a model or provider name),
// de-duplicated against taken with a numeric suffix.
func makeID(base string, taken map[string]bool) string {
	s := slug(base)
	if s == "" {
		s = "model"
	}
	id := s
	for i := 2; taken[id]; i++ {
		id = s + "-" + strconv.Itoa(i)
	}
	return id
}

// slug lowercases s and keeps [a-z0-9], folding separators to single dashes.
func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// loadFile reads config.json if present. A missing or unreadable file yields
// an empty fileConfig (env and defaults still drive Load); a malformed file
// is likewise ignored rather than failing startup, since env can carry every
// field.
func loadFile(path string) fileConfig {
	var fc fileConfig
	b, err := os.ReadFile(path)
	if err != nil {
		return fc
	}
	_ = json.Unmarshal(b, &fc)
	return fc
}

// pick resolves one field with precedence env > file > default.
func pick(envKey, fileVal, def string) string {
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	if fileVal != "" {
		return fileVal
	}
	return def
}

// loadPatrol reads OPSAGENT_PATROL_* variables. Patrol runs read-only
// checks by default, but auto-restart fires only for units explicitly
// listed in OPSAGENT_PATROL_SERVICES, so the default install never acts
// unattended until the operator opts a unit in.
func loadPatrol() PatrolConfig {
	return PatrolConfig{
		Enabled:  getenvBool("OPSAGENT_PATROL", true),
		Interval: getenvDuration("OPSAGENT_PATROL_INTERVAL", 5*time.Minute),
		Checks:   getenvList("OPSAGENT_PATROL_CHECKS", []string{"disk", "load", "key_services"}),
		Services: getenvList("OPSAGENT_PATROL_SERVICES", nil),
		DiskPct:  getenvInt("OPSAGENT_PATROL_DISK_PCT", 90),
		LoadPer:  getenvFloat("OPSAGENT_PATROL_LOAD", 2.0),
	}
}

// defaultStateDir returns a per-user config directory. The system service
// always sets OPSAGENT_STATE_DIR=/var/lib/opsagent explicitly (via the
// systemd unit), so the fallback here is only reached by regular users
// running ops directly — they must not share the service-owned path.
func defaultStateDir() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "opsagent")
	}
	return filepath.Join(os.TempDir(), "opsagent")
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getenvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func getenvFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func getenvBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

func getenvDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return def
}

// getenvList splits a comma-separated variable into trimmed, non-empty
// entries; an unset variable yields def.
func getenvList(key string, def []string) []string {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	var out []string
	for part := range strings.SplitSeq(v, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}
