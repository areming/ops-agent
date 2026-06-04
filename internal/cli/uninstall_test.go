package cli

import (
	"strings"
	"testing"
)

func TestBinaryOnlyPlan(t *testing.T) {
	const exec = "/usr/local/bin/ops"
	const state = "/home/me/.config/opsagent"

	t.Run("purge lists the state dir for deletion", func(t *testing.T) {
		lines := binaryOnlyPlan(exec, state, true)
		joined := strings.Join(lines, "\n")
		if !strings.Contains(joined, exec) {
			t.Errorf("plan missing binary line: %q", joined)
		}
		if !strings.Contains(joined, state) || !strings.Contains(joined, "(--purge)") {
			t.Errorf("purge plan must mark state dir for deletion: %q", joined)
		}
		if strings.Contains(joined, "kept") {
			t.Errorf("purge plan must not say the state dir is kept: %q", joined)
		}
	})

	t.Run("no purge keeps the state dir and hints at --purge", func(t *testing.T) {
		lines := binaryOnlyPlan(exec, state, false)
		joined := strings.Join(lines, "\n")
		if !strings.Contains(joined, state) || !strings.Contains(joined, "kept") {
			t.Errorf("non-purge plan must show the state dir as kept: %q", joined)
		}
		if strings.Contains(joined, "(--purge)") {
			t.Errorf("non-purge plan must not mark state dir for deletion: %q", joined)
		}
	})

	t.Run("unknown state dir lists only the binary", func(t *testing.T) {
		lines := binaryOnlyPlan(exec, "", true)
		if len(lines) != 1 || !strings.Contains(lines[0], exec) {
			t.Errorf("empty state dir should yield binary-only plan, got %v", lines)
		}
	})
}

func TestPurgeWarning(t *testing.T) {
	if w := purgeWarning(false); w != "" {
		t.Errorf("a plain uninstall must not warn about data loss, got %q", w)
	}
	w := purgeWarning(true)
	if w == "" {
		t.Fatal("purge must warn before deleting all data")
	}
	if !strings.Contains(w, "不可恢复") {
		t.Errorf("purge warning should state it is irreversible, got %q", w)
	}
}
