package cli

import (
	"strings"
	"testing"
)

func TestDefaultModel(t *testing.T) {
	for provider, want := range map[string]string{
		"deepseek":  "deepseek-chat",
		"openai":    "gpt-4o",
		"anthropic": "claude-sonnet-4-6",
		"unknown":   "",
	} {
		if got := defaultModel(provider); got != want {
			t.Errorf("defaultModel(%q) = %q, want %q", provider, got, want)
		}
	}
}

func TestIsYes(t *testing.T) {
	for in, want := range map[string]bool{
		"":    true, // empty accepts the [Y/n] default
		"y":   true,
		"YES": true,
		" y ": true,
		"n":   false,
		"no":  false,
		"x":   false,
	} {
		if got := isYes(in); got != want {
			t.Errorf("isYes(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestSetupSummary(t *testing.T) {
	s := setupSummary("web1", "opsagent", "deepseek", "deepseek-chat", "", "", "")
	for _, want := range []string{"web1", "opsagent", "deepseek", "deepseek-chat"} {
		if !strings.Contains(s, want) {
			t.Errorf("summary missing %q:\n%s", want, s)
		}
	}
	for _, omit := range []string{"base URL", "巡检服务", "诊断模型"} {
		if strings.Contains(s, omit) {
			t.Errorf("summary should omit empty %q:\n%s", omit, s)
		}
	}
	full := setupSummary("h", "u", "openai", "m", "http://x", "nginx,sshd", "gpt-4o")
	for _, want := range []string{"base URL", "巡检服务", "nginx,sshd", "诊断模型", "gpt-4o"} {
		if !strings.Contains(full, want) {
			t.Errorf("summary missing %q:\n%s", want, full)
		}
	}
}
