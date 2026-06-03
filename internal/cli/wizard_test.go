package cli

import (
	"slices"
	"strings"
	"testing"
)

func TestMaskSecret(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"a", "*"},
		{"abcd", "****"},             // <=4: fully masked, nothing safe to reveal
		{"abcde", "ab*de"},           // 5..10: head/tail 2
		{"abcdefghij", "ab******ij"}, // boundary at 10
		{"sk-0123456789abcdef", "sk-0***********cdef"}, // >10: head/tail 4
	}
	for _, c := range cases {
		got := maskSecret(c.in)
		if got != c.want {
			t.Errorf("maskSecret(%q) = %q, want %q", c.in, got, c.want)
		}
		// The masked form must never leak the hidden middle, and its rune length
		// must equal the input's so a paste is visibly the right size.
		if len([]rune(got)) != len([]rune(c.in)) {
			t.Errorf("maskSecret(%q) length = %d, want %d", c.in, len([]rune(got)), len([]rune(c.in)))
		}
	}
}

func TestLookupProvider(t *testing.T) {
	cases := map[string]struct {
		id string
		ok bool
	}{
		"1":          {"deepseek", true}, // 1-based menu number
		"2":          {"openai", true},
		"3":          {"anthropic", true},
		"deepseek":   {"deepseek", true},
		"OpenAI":     {"openai", true},    // case-insensitive
		"claude":     {"anthropic", true}, // alias
		" anthropic": {"anthropic", true}, // trimmed
		"kimi":       {"moonshot", true},  // alias
		"":           {"", false},
		"999":        {"", false}, // out of range
		"nope":       {"", false},
	}
	for in, want := range cases {
		e, ok := lookupProvider(in)
		if ok != want.ok || (ok && e.ID != want.id) {
			t.Errorf("lookupProvider(%q) = (%q, %v), want (%q, %v)", in, e.ID, ok, want.id, want.ok)
		}
	}
}

// TestProviderCatalog guards the invariants the rest of the code relies on:
// every entry maps to a real adapter, carries a base URL and default model
// (except the custom escape hatch), and the default model is one of its
// listed models.
func TestProviderCatalog(t *testing.T) {
	validAdapters := []string{"openai", "deepseek", "anthropic"}
	seen := map[string]bool{}
	for _, e := range providerCatalog {
		if e.ID == "" || e.Label == "" {
			t.Errorf("entry %+v missing ID/Label", e)
		}
		if seen[e.ID] {
			t.Errorf("duplicate provider ID %q", e.ID)
		}
		seen[e.ID] = true
		if !slices.Contains(validAdapters, e.Adapter) {
			t.Errorf("provider %q has unknown adapter %q", e.ID, e.Adapter)
		}
		if e.ID == "custom" {
			continue // custom takes base URL and model from the user
		}
		if e.BaseURL == "" {
			t.Errorf("provider %q has no default base URL", e.ID)
		}
		if e.DefaultModel == "" {
			t.Errorf("provider %q has no default model", e.ID)
		}
		if len(e.Models) > 0 && !slices.Contains(e.Models, e.DefaultModel) {
			t.Errorf("provider %q default model %q not in its model list", e.ID, e.DefaultModel)
		}
	}
	// The historical 1/2/3 order is part of the UI contract.
	for i, id := range []string{"deepseek", "openai", "anthropic"} {
		if providerCatalog[i].ID != id {
			t.Errorf("providerCatalog[%d].ID = %q, want %q", i, providerCatalog[i].ID, id)
		}
	}
}

// TestProviderBaseURLs sanity-checks that catalog base URLs are well-formed
// https endpoints (a typo here silently breaks a beginner's first request).
func TestProviderBaseURLs(t *testing.T) {
	for _, e := range providerCatalog {
		if e.BaseURL == "" {
			continue
		}
		if !strings.HasPrefix(e.BaseURL, "https://") {
			t.Errorf("provider %q base URL %q is not https", e.ID, e.BaseURL)
		}
		if strings.HasSuffix(e.BaseURL, "/") {
			t.Errorf("provider %q base URL %q has a trailing slash", e.ID, e.BaseURL)
		}
	}
}
