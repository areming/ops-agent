// Package config loads agent settings. M1 reads only environment
// variables to stay dependency-free; a TOML file arrives in a later
// milestone when there is more to configure.
package config

import "os"

type Config struct {
	Provider string // openai | deepseek | anthropic
	Model    string
	APIKey   string
	BaseURL  string // optional override for OpenAI-compatible/Anthropic
}

// Load reads OPSAGENT_* environment variables.
func Load() Config {
	return Config{
		Provider: getenv("OPSAGENT_PROVIDER", "openai"),
		Model:    os.Getenv("OPSAGENT_MODEL"),
		APIKey:   os.Getenv("OPSAGENT_API_KEY"),
		BaseURL:  os.Getenv("OPSAGENT_BASE_URL"),
	}
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
