package model

import "testing"

func TestKnownModels(t *testing.T) {
	if got := KnownModels("deepseek"); len(got) == 0 {
		t.Error("deepseek returned no known models")
	}
	if got := KnownModels("claude"); len(got) == 0 {
		t.Error("claude alias returned no known models")
	}
	if got := KnownModels("nonsense"); got != nil {
		t.Errorf("unknown provider returned %v, want nil", got)
	}
}
