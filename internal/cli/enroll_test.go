package cli

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestArchFromUname(t *testing.T) {
	cases := map[string]string{
		"x86_64\n": "amd64",
		"amd64":    "amd64",
		"aarch64":  "arm64",
		"arm64\n":  "arm64",
	}
	for in, want := range cases {
		got, err := archFromUname(in)
		if err != nil {
			t.Errorf("archFromUname(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("archFromUname(%q) = %q, want %q", in, got, want)
		}
	}
	if _, err := archFromUname("riscv64"); err == nil {
		t.Error("archFromUname(riscv64) should error")
	}
}

func TestBuildSudoers(t *testing.T) {
	got := buildSudoers("opsagent")
	want := "opsagent ALL=(root) NOPASSWD: /usr/bin/systemctl, /usr/bin/journalctl"
	if !strings.Contains(got, want) {
		t.Errorf("sudoers missing whitelist line:\n%s", got)
	}
	// Nothing broader than service management should slip in.
	for _, forbidden := range []string{"ALL:ALL", "NOPASSWD:ALL", "apt", "bash"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("sudoers unexpectedly contains %q:\n%s", forbidden, got)
		}
	}
}

func TestBuildSystemdUnit(t *testing.T) {
	unit := buildSystemdUnit(EnrollOptions{
		User: "opsagent", Provider: "deepseek", Model: "deepseek-chat",
		Services: "nginx,sshd", DiagModel: "deepseek-v4-pro",
	})
	for _, want := range []string{
		"User=opsagent",
		"Group=opsagent",
		"RuntimeDirectory=opsagent",
		"Environment=OPSAGENT_STATE_DIR=/var/lib/opsagent",
		"Environment=OPSAGENT_PATROL_SERVICES=nginx,sshd",
		"Environment=OPSAGENT_DIAG_MODEL=deepseek-v4-pro",
		"ExecStart=/usr/local/bin/ops serve --socket /run/opsagent/agent.sock",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit missing %q:\n%s", want, unit)
		}
	}
	// The chat model is no longer pinned in the unit — it lives in config.json
	// as a switchable profile, so an in-session /model switch survives restart.
	for _, forbidden := range []string{"OPSAGENT_PROVIDER=", "OPSAGENT_MODEL=", "OPSAGENT_BASE_URL=", "OPSAGENT_API_KEY"} {
		if strings.Contains(unit, forbidden) {
			t.Errorf("unit must not pin %q (now seeded into config.json):\n%s", forbidden, unit)
		}
	}
}

func TestBuildSystemdUnitOmitsEmptyOptionals(t *testing.T) {
	unit := buildSystemdUnit(EnrollOptions{User: "opsagent", Provider: "openai"})
	if strings.Contains(unit, "OPSAGENT_PATROL_SERVICES=") {
		t.Errorf("unit should omit empty patrol services:\n%s", unit)
	}
	if strings.Contains(unit, "OPSAGENT_DIAG_MODEL=") {
		t.Errorf("unit should omit empty diag model:\n%s", unit)
	}
}

func TestBuildBootstrap(t *testing.T) {
	opts := EnrollOptions{User: "opsagent", Provider: "deepseek", Model: "deepseek-chat", APIKey: "sk-secret"}
	script := buildBootstrap(opts, "BIN_SRC=/tmp/opsagent-enroll-123")

	for _, want := range []string{
		"set -euo pipefail",
		"useradd --system",
		"BIN_SRC=/tmp/opsagent-enroll-123",
		`install -m 0755 "$BIN_SRC" /usr/local/bin/ops`,
		"ln -sf /usr/local/bin/ops /usr/local/bin/opsagent",
		"visudo -cf /tmp/opsagent.sudoers",
		"runuser -u \"$SVC_USER\" -- env OPSAGENT_STATE_DIR=\"$STATE\" /usr/local/bin/ops _seed --provider 'deepseek' --model 'deepseek-chat'",
		"usermod -aG \"$SVC_USER\" \"$SUDO_USER\"",
		"systemctl enable opsagent.service",
		// restart (not just enable --now) so re-running enroll actually
		// picks up a new binary instead of leaving the old process running.
		"systemctl restart opsagent.service",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("bootstrap missing %q", want)
		}
	}
	// The key is embedded base64-encoded, never in plaintext.
	if strings.Contains(script, "sk-secret") {
		t.Errorf("bootstrap leaks plaintext key:\n%s", script)
	}
	if !strings.Contains(script, base64.StdEncoding.EncodeToString([]byte("sk-secret"))) {
		t.Error("bootstrap missing base64-encoded key")
	}
}

func TestBuildBootstrapFetch(t *testing.T) {
	// A fetch-mode obtain snippet (curl + checksum verify) must be embedded
	// verbatim and the install must read from $BIN_SRC.
	obtain := "BIN_SRC=/tmp/ops-dl-$$\ncurl -fsSL https://example/ops-linux-amd64 -o \"$BIN_SRC\"\necho \"abc123  $BIN_SRC\" | sha256sum -c -"
	script := buildBootstrap(EnrollOptions{User: "opsagent"}, obtain)
	for _, want := range []string{
		"curl -fsSL https://example/ops-linux-amd64",
		"sha256sum -c -",
		`install -m 0755 "$BIN_SRC" /usr/local/bin/ops`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("fetch bootstrap missing %q", want)
		}
	}
}

func TestReleaseFetchSnippet(t *testing.T) {
	s := releaseFetchSnippet("https://example/ops-linux-amd64", "abc123")
	for _, want := range []string{
		"--connect-timeout 10", // fail fast on a blackholed connection
		"--max-time 600",       // hard ceiling on a stalled transfer
		"-#",                   // visible progress, not silent
		"https://example/ops-linux-amd64",
		"abc123  $BIN_SRC",
		"sha256sum -c -",
		"build.ps1", // offline-path hint shown on failure
	} {
		if !strings.Contains(s, want) {
			t.Errorf("fetch snippet missing %q:\n%s", want, s)
		}
	}
	// The old silent, timeout-less form must be gone — that was the hang.
	if strings.Contains(s, "-fsSL") {
		t.Errorf("snippet still uses silent curl (-fsSL):\n%s", s)
	}
}

func TestLocalBinary(t *testing.T) {
	// An explicit --bin that does not exist is an error.
	if _, err := localBinary("/no/such/ops", "amd64"); err == nil {
		t.Error("localBinary with missing explicit path should error")
	}
	// No explicit path and no dist build -> empty (caller fetches a release).
	// Run from a temp dir so a developer's real dist/ doesn't interfere.
	t.Chdir(t.TempDir())
	got, err := localBinary("", "amd64")
	if err != nil {
		t.Fatalf("localBinary: %v", err)
	}
	if got != "" {
		t.Errorf("localBinary without dist = %q, want empty", got)
	}
}
