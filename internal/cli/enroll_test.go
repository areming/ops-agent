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
		"Environment=OPSAGENT_PROVIDER=deepseek",
		"Environment=OPSAGENT_MODEL=deepseek-chat",
		"Environment=OPSAGENT_PATROL_SERVICES=nginx,sshd",
		"Environment=OPSAGENT_DIAG_MODEL=deepseek-v4-pro",
		"ExecStart=/usr/local/bin/opsagent serve --socket /run/opsagent/agent.sock",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit missing %q:\n%s", want, unit)
		}
	}
	// The API key must never appear in the unit.
	if strings.Contains(unit, "OPSAGENT_API_KEY") {
		t.Errorf("unit leaks API key env:\n%s", unit)
	}
}

func TestBuildSystemdUnitOmitsEmptyOptionals(t *testing.T) {
	unit := buildSystemdUnit(EnrollOptions{User: "opsagent", Provider: "openai"})
	if strings.Contains(unit, "OPSAGENT_MODEL=") {
		t.Errorf("unit should omit empty model:\n%s", unit)
	}
	if strings.Contains(unit, "OPSAGENT_BASE_URL=") {
		t.Errorf("unit should omit empty base url:\n%s", unit)
	}
	if strings.Contains(unit, "OPSAGENT_PATROL_SERVICES=") {
		t.Errorf("unit should omit empty patrol services:\n%s", unit)
	}
	if strings.Contains(unit, "OPSAGENT_DIAG_MODEL=") {
		t.Errorf("unit should omit empty diag model:\n%s", unit)
	}
}

func TestBuildBootstrap(t *testing.T) {
	opts := EnrollOptions{User: "opsagent", Provider: "deepseek", Model: "deepseek-chat", APIKey: "sk-secret"}
	script := buildBootstrap(opts, "/tmp/opsagent-enroll-123")

	for _, want := range []string{
		"set -euo pipefail",
		"useradd --system",
		"install -m 0755 /tmp/opsagent-enroll-123 /usr/local/bin/opsagent",
		"visudo -cf /tmp/opsagent.sudoers",
		"runuser -u \"$SVC_USER\" -- env OPSAGENT_STATE_DIR=\"$STATE\" /usr/local/bin/opsagent key set api_key",
		"usermod -aG \"$SVC_USER\" \"$SUDO_USER\"",
		"systemctl enable --now opsagent.service",
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
