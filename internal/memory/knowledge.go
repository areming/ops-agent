package memory

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// LoadKnowledge reads every *.md file in dir (sorted by name) and joins
// them into one block to fold into the system prompt. A missing dir yields
// an empty string, not an error, so knowledge is purely optional.
func LoadKnowledge(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}

	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".md") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	var parts []string
	for _, name := range names {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return "", err
		}
		if text := strings.TrimSpace(string(b)); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n"), nil
}
