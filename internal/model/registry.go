package model

import "fmt"

// New constructs a Provider by name. "deepseek" is a convenience alias
// for the OpenAI-compatible adapter pointed at DeepSeek's default host.
func New(provider, apiKey, baseURL, modelName string) (Provider, error) {
	switch provider {
	case "openai", "openai-compatible":
		return NewOpenAI(apiKey, baseURL, modelName), nil
	case "deepseek":
		if baseURL == "" {
			baseURL = "https://api.deepseek.com"
		}
		return NewOpenAI(apiKey, baseURL, modelName), nil
	case "anthropic", "claude":
		return NewAnthropic(apiKey, baseURL, modelName), nil
	default:
		return nil, fmt.Errorf("unknown provider %q (want openai|deepseek|anthropic)", provider)
	}
}
