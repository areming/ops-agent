package cli

import (
	"bufio"
	"fmt"
	"os"

	"github.com/areming/ops-agent/internal/config"
	"github.com/areming/ops-agent/internal/secret"
)

// onboardLocal runs first-time local setup: it asks for a provider, model,
// optional base URL and the API key, seals the key in the encrypted keystore,
// and persists the model selection to config.json. After it returns,
// config.Load reflects the choices, so RunLocal can start the conversation.
func onboardLocal() error {
	wizardTitle("配置模型", "ops 还没配置模型，先设置一下（本地，仅这台机）")
	r := bufio.NewReader(os.Stdin)

	entry, modelName, baseURL, err := collectModel(r)
	if err != nil {
		return err
	}
	apiKey, err := promptAPIKey(r)
	if err != nil {
		return err
	}
	if apiKey == "" {
		return fmt.Errorf("空 key，已取消")
	}

	if err := persistLocalConfig(entry.Adapter, modelName, baseURL, entry.Label, apiKey); err != nil {
		return err
	}

	fmt.Print("✓ 已保存，进入对话。\n\n")
	return nil
}

// persistLocalConfig saves the model selection as a profile and seals its API
// key under the profile's own keystore entry, so the next config.Load picks
// the choice up. On a seal failure it rolls back the profile so the list never
// points at a missing key.
func persistLocalConfig(provider, modelName, baseURL, label, apiKey string) error {
	cfg := config.Load()
	stored, err := config.AddProfile(cfg.StateDir, config.Profile{
		Label: label, Provider: provider, Model: modelName, BaseURL: baseURL,
	})
	if err != nil {
		return err
	}
	ks, err := secret.Open(cfg.KeystorePath, cfg.MasterKeyPath)
	if err != nil {
		_, _ = config.DeleteProfile(cfg.StateDir, stored.ID)
		return err
	}
	if err := ks.Set(stored.KeyRef, apiKey); err != nil {
		_, _ = config.DeleteProfile(cfg.StateDir, stored.ID)
		return err
	}
	return nil
}
