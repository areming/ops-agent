// Package config loads agent settings. M1 reads only environment
// variables to stay dependency-free; a TOML file arrives in a later
// milestone when there is more to configure.
package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
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

	// StateDir holds the agent's at-rest state: secret keystore, master
	// key, and knowledge files. Per-file paths derive from it.
	StateDir      string
	KeystorePath  string
	MasterKeyPath string
	KnowledgeDir  string
	HistoryDepth  int // messages reloaded into a new session from history
}

// Load reads OPSAGENT_* environment variables.
func Load() Config {
	stateDir := getenv("OPSAGENT_STATE_DIR", defaultStateDir())
	return Config{
		Provider:      getenv("OPSAGENT_PROVIDER", "openai"),
		Model:         os.Getenv("OPSAGENT_MODEL"),
		APIKey:        os.Getenv("OPSAGENT_API_KEY"),
		BaseURL:       os.Getenv("OPSAGENT_BASE_URL"),
		DBPath:        getenv("OPSAGENT_DB", filepath.Join(stateDir, "state.db")),
		StateDir:      stateDir,
		KeystorePath:  filepath.Join(stateDir, "keystore.json"),
		MasterKeyPath: filepath.Join(stateDir, "master.key"),
		KnowledgeDir:  getenv("OPSAGENT_KNOWLEDGE_DIR", filepath.Join(stateDir, "knowledge")),
		HistoryDepth:  getenvInt("OPSAGENT_HISTORY", 50),
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
