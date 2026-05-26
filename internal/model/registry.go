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

// KnownModels returns common model names for a provider, shown by /models as
// a convenience. Any model the provider accepts also works via
// `/models <name>`; this list is not exhaustive.
func KnownModels(provider string) []string {
	switch provider {
	case "openai", "openai-compatible":
		return []string{"gpt-4o", "gpt-4o-mini", "o3-mini"}
	case "deepseek":
		return []string{"deepseek-chat", "deepseek-reasoner"}
	case "anthropic", "claude":
		return []string{"claude-sonnet-4-6", "claude-opus-4-7", "claude-haiku-4-5-20251001"}
	default:
		return nil
	}
}
