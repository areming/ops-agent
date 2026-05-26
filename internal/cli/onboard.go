package cli

import (
	"bufio"
	"fmt"
	"os"

	"github.com/areming/ops-agent/internal/config"
	"github.com/areming/ops-agent/internal/secret"
)

// localKeySecretName is the keystore entry the agent reads its model API key
// from. It must match the name the agent and `key set` use ("api_key").
const localKeySecretName = "api_key"

// onboardLocal runs first-time local setup: it asks for a provider, model,
// optional base URL and the API key, seals the key in the encrypted keystore,
// and persists the model selection to config.json. After it returns,
// config.Load reflects the choices, so RunLocal can start the conversation.
func onboardLocal() error {
	fmt.Print("ops 还没配置模型，先设置一下（本地，仅这台机）。\n\n")
	r := bufio.NewReader(os.Stdin)

	provider, err := promptProvider(r)
	if err != nil {
		return err
	}
	modelName, err := prompt(r, "模型名", defaultModel(provider))
	if err != nil {
		return err
	}
	baseURL, err := prompt(r, "自定义 base URL（回车跳过）", "")
	if err != nil {
		return err
	}
	apiKey, err := promptSecret(r, "粘贴 API key（不回显）")
	if err != nil {
		return err
	}
	if apiKey == "" {
		return fmt.Errorf("空 key，已取消")
	}

	if err := persistLocalConfig(provider, modelName, baseURL, apiKey); err != nil {
		return err
	}

	fmt.Print("✓ 已保存，进入对话。\n\n")
	return nil
}

// persistLocalConfig seals the API key in the keystore and writes the model
// selection to config.json, so the next config.Load picks them up.
func persistLocalConfig(provider, modelName, baseURL, apiKey string) error {
	cfg := config.Load()
	ks, err := secret.Open(cfg.KeystorePath, cfg.MasterKeyPath)
	if err != nil {
		return err
	}
	if err := ks.Set(localKeySecretName, apiKey); err != nil {
		return err
	}

	cfg.Provider = provider
	cfg.Model = modelName
	cfg.BaseURL = baseURL
	return config.Save(cfg)
}
