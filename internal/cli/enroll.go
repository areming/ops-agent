package cli

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// AgentSocketPath is the fixed socket an enrolled agent listens on. serve
// and _bridge default to it on Linux, so `connect <host>` needs no flag.
const AgentSocketPath = "/run/opsagent/agent.sock"

// Install paths on the managed host. enroll lays these out; the systemd
// unit and config defaults agree with them.
const (
	installBinPath = "/usr/local/bin/ops"
	// legacyBinPath is a symlink to installBinPath so anything still invoking
	// the old `opsagent` name (e.g. a not-yet-upgraded local client) keeps
	// working after the rename.
	legacyBinPath  = "/usr/local/bin/opsagent"
	stateDir       = "/var/lib/opsagent"
	runtimeDirName = "opsagent" // systemd RuntimeDirectory -> /run/opsagent
	sudoersPath    = "/etc/sudoers.d/opsagent"
	unitPath       = "/etc/systemd/system/opsagent.service"
)

// EnrollOptions configures a deployment. APIKey is provisioned into the
// remote keystore; it is never written to the unit or to disk in plaintext.
type EnrollOptions struct {
	User      string // dedicated service user
	Provider  string
	Model     string
	BaseURL   string
	BinPath   string // local linux binary; empty -> dist/ops-linux-<arch>, else fetch release
	APIKey    string
	Services  string // comma-separated units patrol watches and may auto-restart
	DiagModel string // optional diagnosis model (reuses the main provider/key)
	Version   string // build version; used to fetch a release when no local binary exists
}

// Enroll deploys the agent to host over SSH: detect the architecture, copy
// the matching binary, then run a privileged bootstrap script that creates
// the service user, installs the binary, sudoers, and systemd unit, stores
// the API key encrypted, and starts the agent.
//
// The SSH user must be able to run sudo non-interactively (NOPASSWD) or be
// root; `sudo -n` is used so a password requirement fails fast and clearly.
func Enroll(host string, opts EnrollOptions) error {
	arch, err := detectArch(host)
	if err != nil {
		return err
	}

	obtain, err := obtainStep(host, arch, opts)
	if err != nil {
		return err
	}

	script := buildBootstrap(opts, obtain)
	fmt.Fprintf(os.Stderr, "→ running bootstrap on %s (sudo)\n", host)
	cmd := exec.Command("ssh", host, "sudo", "-n", "bash", "-s")
	cmd.Stdin = strings.NewReader(script)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("bootstrap failed (ensure the SSH user has passwordless sudo or is root): %w", err)
	}

	fmt.Fprintf(os.Stderr, "✓ enrolled. run: ops connect %s\n", host)
	fmt.Fprintln(os.Stderr, "  (if connect is denied, re-login so your new opsagent group membership applies)")
	return nil
}

// detectArch reads the remote machine architecture and maps it to a Go arch.
func detectArch(host string) (string, error) {
	out, err := exec.Command("ssh", host, "uname", "-m").Output()
	if err != nil {
		return "", fmt.Errorf("detect remote arch: %w", err)
	}
	return archFromUname(string(out))
}

// archFromUname maps `uname -m` output to a Go GOARCH the build script emits.
func archFromUname(uname string) (string, error) {
	switch strings.TrimSpace(uname) {
	case "x86_64", "amd64":
		return "amd64", nil
	case "aarch64", "arm64":
		return "arm64", nil
	default:
		return "", fmt.Errorf("unsupported remote architecture %q (have amd64/arm64 builds)", strings.TrimSpace(uname))
	}
}

// obtainStep returns the shell snippet that places the agent binary at
// $BIN_SRC on the host. It prefers a local binary (explicit --bin or a
// dist/ build) and scp's it — which works offline; otherwise it has the host
// fetch the matching release over HTTPS and verify it against the checksum
// this side fetched, so a tampered binary is rejected before it runs as root.
func obtainStep(host, arch string, opts EnrollOptions) (string, error) {
	local, err := localBinary(opts.BinPath, arch)
	if err != nil {
		return "", err
	}
	if local != "" {
		remoteTmp := fmt.Sprintf("/tmp/opsagent-enroll-%d", time.Now().UnixNano())
		fmt.Fprintf(os.Stderr, "→ copying %s to %s:%s\n", local, host, remoteTmp)
		if err := run("scp", local, host+":"+remoteTmp); err != nil {
			return "", fmt.Errorf("copy binary: %w", err)
		}
		return "BIN_SRC=" + remoteTmp, nil
	}

	if opts.Version == "" || opts.Version == "dev" {
		return "", fmt.Errorf("no local binary dist/ops-linux-%s and this is an unversioned build, "+
			"so it can't fetch a release; run ./build.ps1 first, or use a released ops", arch)
	}
	sum, err := fetchChecksum(opts.Version, arch)
	if err != nil {
		return "", fmt.Errorf("fetch release checksum for %s: %w", opts.Version, err)
	}
	url := releaseBinURL(opts.Version, arch)
	fmt.Fprintf(os.Stderr, "→ %s will fetch %s and verify sha256\n", host, url)
	return fmt.Sprintf("BIN_SRC=/tmp/ops-dl-$$\n"+
		"curl -fsSL %s -o \"$BIN_SRC\"\n"+
		"echo \"%s  $BIN_SRC\" | sha256sum -c -", url, sum), nil
}

// localBinary returns the local linux binary to scp, or "" if none exists (so
// enroll should fetch a release). An explicit --bin that is missing is an
// error.
func localBinary(explicit, arch string) (string, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", fmt.Errorf("agent binary %q not found: %w", explicit, err)
		}
		return explicit, nil
	}
	path := fmt.Sprintf("dist/ops-linux-%s", arch)
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	return "", nil
}

// buildSudoers returns the NOPASSWD whitelist: service management only.
// Writes still pass through the safety gate's confirmation regardless.
func buildSudoers(user string) string {
	return fmt.Sprintf("# Managed by opsagent enroll. Service management only.\n"+
		"%s ALL=(root) NOPASSWD: /usr/bin/systemctl, /usr/bin/journalctl\n", user)
}

// buildSystemdUnit returns the service unit. The API key is intentionally
// absent: it lives encrypted in the keystore, not in the environment.
func buildSystemdUnit(opts EnrollOptions) string {
	var env strings.Builder
	fmt.Fprintf(&env, "Environment=OPSAGENT_STATE_DIR=%s\n", stateDir)
	fmt.Fprintf(&env, "Environment=OPSAGENT_PROVIDER=%s\n", opts.Provider)
	if opts.Model != "" {
		fmt.Fprintf(&env, "Environment=OPSAGENT_MODEL=%s\n", opts.Model)
	}
	if opts.BaseURL != "" {
		fmt.Fprintf(&env, "Environment=OPSAGENT_BASE_URL=%s\n", opts.BaseURL)
	}
	if opts.Services != "" {
		fmt.Fprintf(&env, "Environment=OPSAGENT_PATROL_SERVICES=%s\n", opts.Services)
	}
	if opts.DiagModel != "" {
		fmt.Fprintf(&env, "Environment=OPSAGENT_DIAG_MODEL=%s\n", opts.DiagModel)
	}
	return fmt.Sprintf(`[Unit]
Description=opsagent resident ops assistant
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=%s
Group=%s
RuntimeDirectory=%s
RuntimeDirectoryMode=0750
%sExecStart=%s serve --socket %s
Restart=on-failure
RestartSec=2

[Install]
WantedBy=multi-user.target
`, opts.User, opts.User, runtimeDirName, env.String(), installBinPath, AgentSocketPath)
}

// buildBootstrap returns the idempotent root script run over SSH. obtain is a
// snippet that leaves the agent binary at $BIN_SRC (an scp'd temp path, or a
// curl+verify). The API key is base64-encoded so it carries no shell
// metacharacters and is piped straight into `key set` (never written to a file).
func buildBootstrap(opts EnrollOptions, obtain string) string {
	keyB64 := base64.StdEncoding.EncodeToString([]byte(opts.APIKey))
	return fmt.Sprintf(`set -euo pipefail
SVC_USER=%[1]s
STATE=%[2]s

id -u "$SVC_USER" >/dev/null 2>&1 || useradd --system --home-dir "$STATE" --shell /usr/sbin/nologin "$SVC_USER"

%[3]s
install -m 0755 "$BIN_SRC" %[4]s
ln -sf %[4]s %[10]s
rm -f "$BIN_SRC"

mkdir -p "$STATE/knowledge"
chown -R "$SVC_USER:$SVC_USER" "$STATE"
chmod 0750 "$STATE"

umask 077
cat > /tmp/opsagent.sudoers <<'SUDOERS'
%[5]sSUDOERS
visudo -cf /tmp/opsagent.sudoers
install -m 0440 -o root -g root /tmp/opsagent.sudoers %[6]s
rm -f /tmp/opsagent.sudoers

cat > %[7]s <<'UNIT'
%[8]sUNIT

echo %[9]s | base64 -d | runuser -u "$SVC_USER" -- env OPSAGENT_STATE_DIR="$STATE" %[4]s key set api_key

if [ -n "${SUDO_USER:-}" ]; then usermod -aG "$SVC_USER" "$SUDO_USER"; fi

systemctl daemon-reload
systemctl enable --now opsagent.service
echo "opsagent service started"
`,
		opts.User,               // 1
		stateDir,                // 2
		obtain,                  // 3
		installBinPath,          // 4
		buildSudoers(opts.User), // 5
		sudoersPath,             // 6
		unitPath,                // 7
		buildSystemdUnit(opts),  // 8
		keyB64,                  // 9
		legacyBinPath,           // 10
	)
}

// run executes a command, streaming its output to the user.
func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
