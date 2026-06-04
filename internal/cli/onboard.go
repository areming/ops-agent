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
// key, so the next config.Load picks the choice up.
func persistLocalConfig(provider, modelName, baseURL, label, apiKey string) error {
	_, err := saveModelProfile(label, provider, modelName, baseURL, apiKey)
	return err
}

// saveModelProfile upserts a model profile and seals its API key under the
// profile's keystore entry (so re-adding the same provider/model rotates its
// key rather than duplicating). On a seal failure it rolls back a freshly
// added profile so config.json never points at a missing key.
func saveModelProfile(label, provider, modelName, baseURL, apiKey string) (config.Profile, error) {
	cfg := config.Load()
	stored, existed, err := config.UpsertProfile(cfg.StateDir, config.Profile{
		Label: label, Provider: provider, Model: modelName, BaseURL: baseURL,
	})
	if err != nil {
		return config.Profile{}, err
	}
	ks, err := secret.Open(cfg.KeystorePath, cfg.MasterKeyPath)
	if err != nil {
		if !existed {
			_, _ = config.DeleteProfile(cfg.StateDir, stored.ID)
		}
		return config.Profile{}, err
	}
	if err := ks.Set(stored.KeyRef, apiKey); err != nil {
		if !existed {
			_, _ = config.DeleteProfile(cfg.StateDir, stored.ID)
		}
		return config.Profile{}, err
	}
	return stored, nil
}

// Seed writes an enrolled host's initial model profile into config.json and
// seals its key. enroll runs it (as the service user, via `ops _seed`) so the
// daemon's config.json — not the systemd unit — is the source of truth for the
// active model, which lets an in-session /model switch persist across restart.
// It is idempotent (see saveModelProfile): re-running enroll reseals/activates
// the matching profile instead of piling up duplicates.
func Seed(provider, modelName, baseURL, apiKey string) error {
	_, err := saveModelProfile("", provider, modelName, baseURL, apiKey)
	return err
}
