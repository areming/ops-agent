// Package config loads agent settings. M1 reads only environment
// variables to stay dependency-free; a TOML file arrives in a later
// milestone when there is more to configure.
package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// prodStateDir is where a Linux service install keeps its state. enroll
// provisions it owned by the dedicated opsagent user.
const prodStateDir = "/var/lib/opsagent"

type Config struct {
	Provider string // openai | deepseek | anthropic
	Model    string
	APIKey   string // optional plaintext override; empty means read from the keystore
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

// Load reads OPSAGENT_* environment variables.
func Load() Config {
	stateDir := getenv("OPSAGENT_STATE_DIR", defaultStateDir())
	return Config{
		Provider:      getenv("OPSAGENT_PROVIDER", "openai"),
		Model:         os.Getenv("OPSAGENT_MODEL"),
		APIKey:        os.Getenv("OPSAGENT_API_KEY"),
		BaseURL:       os.Getenv("OPSAGENT_BASE_URL"),
		DiagProvider:  getenv("OPSAGENT_DIAG_PROVIDER", getenv("OPSAGENT_PROVIDER", "openai")),
		DiagModel:     getenv("OPSAGENT_DIAG_MODEL", os.Getenv("OPSAGENT_MODEL")),
		DiagBaseURL:   getenv("OPSAGENT_DIAG_BASE_URL", os.Getenv("OPSAGENT_BASE_URL")),
		DBPath:        getenv("OPSAGENT_DB", filepath.Join(stateDir, "state.db")),
		StateDir:      stateDir,
		KeystorePath:  filepath.Join(stateDir, "keystore.json"),
		MasterKeyPath: filepath.Join(stateDir, "master.key"),
		KnowledgeDir:  getenv("OPSAGENT_KNOWLEDGE_DIR", filepath.Join(stateDir, "knowledge")),
		HistoryDepth:  getenvInt("OPSAGENT_HISTORY", 50),
		Patrol:        loadPatrol(),
	}
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

// defaultStateDir is the fixed service path on Linux (where the agent runs
// as a system service) and a per-user dir elsewhere for development.
func defaultStateDir() string {
	if runtime.GOOS == "linux" {
		return prodStateDir
	}
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
