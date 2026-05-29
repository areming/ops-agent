package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/areming/ops-agent/internal/version"
)

// Update checks for a newer release and, unless checkOnly is true, downloads
// and installs it over the running binary. On Linux it prints a reminder to
// restart the systemd service if one is active.
func Update(checkOnly bool) error {
	fmt.Println("checking latest release...")
	latest, err := LatestReleaseVersion()
	if err != nil {
		return fmt.Errorf("fetch latest version: %w", err)
	}

	current := version.Value
	fmt.Printf("current: %s  latest: %s\n", current, latest)

	if current == latest {
		fmt.Println("already up to date")
		return nil
	}
	if checkOnly {
		fmt.Printf("update available: %s → %s\n", current, latest)
		return nil
	}

	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate binary: %w", err)
	}

	goos := runtime.GOOS
	goarch := runtime.GOARCH
	asset := releaseBinAsset(goos, goarch)
	dlURL := releaseBinURLForPlatform(latest, goos, goarch)

	fmt.Printf("fetching checksum for %s...\n", asset)
	sums, err := httpGetString(releaseSumsURL(latest))
	if err != nil {
		return fmt.Errorf("fetch SHA256SUMS: %w", err)
	}
	wantHash, err := parseChecksum(sums, asset)
	if err != nil {
		return fmt.Errorf("parse checksum: %w", err)
	}

	fmt.Printf("downloading %s...\n", asset)
	tmp, err := downloadToTemp(dlURL)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer os.Remove(tmp)

	if err := verifyFile(tmp, wantHash); err != nil {
		return fmt.Errorf("checksum mismatch: %w", err)
	}
	fmt.Println("checksum ok")

	if err := os.Chmod(tmp, 0o755); err != nil {
		return err
	}
	if err := replaceBinary(execPath, tmp); err != nil {
		return fmt.Errorf("replace binary: %w", err)
	}

	fmt.Printf("updated %s → %s\n", current, latest)

	if runtime.GOOS == "linux" {
		suggestServiceRestart()
	}
	return nil
}

// maybeOfferUpdate checks the agent already installed on host against the
// latest release and, when they differ, offers to update it in place before
// the session starts. Probing is best-effort: any failure (an SSH hiccup, no
// GitHub reachability, a "dev" build) silently skips the offer, because a
// version check must never block an operator from connecting.
func maybeOfferUpdate(host, bin string) {
	remote, err := RemoteVersion(host, bin)
	if err != nil {
		return
	}
	latest, err := LatestReleaseVersion()
	if err != nil {
		return
	}
	if !shouldOfferUpdate(remote, latest) {
		return
	}
	fmt.Fprintf(os.Stderr, "%s 上的 ops 是 %s，最新 release 是 %s。\n", host, remote, latest)
	if !promptYesNo(fmt.Sprintf("现在更新 %s 到 %s? [Y/n] ", host, latest)) {
		return
	}
	if err := updateRemoteHost(host, latest); err != nil {
		fmt.Fprintf(os.Stderr, "✗ 更新失败（跳过，继续用当前版本连接）：%v\n", err)
		return
	}
	fmt.Fprintf(os.Stderr, "✓ 已更新并重启 %s 上的 opsagent\n", host)
}

// shouldOfferUpdate reports whether an update should be offered: both versions
// are known, the remote is a released build (not an unversioned "dev" one we
// can't compare), and they differ.
func shouldOfferUpdate(remote, latest string) bool {
	if remote == "" || latest == "" || remote == "dev" {
		return false
	}
	return remote != latest
}

// RemoteVersion returns the version string the agent binary on host reports.
func RemoteVersion(host, bin string) (string, error) {
	out, err := exec.Command("ssh", host, bin, "version").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// updateRemoteHost replaces the agent binary on host with `version` and
// restarts the service. It deliberately does NOT run the remote `ops update`:
// an old agent may predate that subcommand entirely (it just errors "unknown
// command"). Instead it obtains the binary the same way enroll does — scp a
// local dist build if present, otherwise have the host curl the release and
// verify its sha256 — so the update works regardless of the remote's version.
func updateRemoteHost(host, version string) error {
	arch, err := detectArch(host)
	if err != nil {
		return err
	}
	obtain, err := obtainStep(host, arch, EnrollOptions{Version: version})
	if err != nil {
		return err
	}
	cmd := exec.Command("ssh", host, "sudo", "-n", "bash", "-s")
	cmd.Stdin = strings.NewReader(buildUpdateScript(obtain))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("remote update failed (ensure passwordless sudo or root): %w", err)
	}
	return nil
}

// buildUpdateScript returns the root script that installs the agent binary left
// at $BIN_SRC by obtain (an scp'd path or a curl+verify snippet) over the
// existing one and restarts the service. Unlike enroll's bootstrap it touches
// nothing else — no user, sudoers, unit, or key — because this is an update.
func buildUpdateScript(obtain string) string {
	return fmt.Sprintf(`set -euo pipefail
%[1]s
install -m 0755 "$BIN_SRC" %[2]s
ln -sf %[2]s %[3]s
rm -f "$BIN_SRC"
systemctl restart opsagent.service
echo "opsagent updated and restarted"`, obtain, installBinPath, legacyBinPath)
}

// downloadToTemp fetches url into a temporary file and returns its path.
func downloadToTemp(url string) (string, error) {
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	f, err := os.CreateTemp("", "ops-update-*")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// verifyFile computes the SHA-256 of path and compares it to wantHex.
func verifyFile(path, wantHex string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != wantHex {
		return fmt.Errorf("got %s, want %s", got, wantHex)
	}
	return nil
}

// replaceBinary atomically replaces target with src.
//
// On Windows a running exe cannot be overwritten but can be renamed, so we
// rename the current binary to target+".old" first, then move src into place.
// The .old file is removed on the next update attempt.
//
// On Unix os.Rename is atomic within a filesystem; for cross-device temp dirs
// we fall back to a copy-then-rename in the target directory.
func replaceBinary(target, src string) error {
	if runtime.GOOS == "windows" {
		old := target + ".old"
		_ = os.Remove(old) // clean up previous update leftover
		if err := os.Rename(target, old); err != nil {
			return fmt.Errorf("rename current binary: %w", err)
		}
		if err := os.Rename(src, target); err != nil {
			_ = os.Rename(old, target) // restore
			return fmt.Errorf("place new binary: %w", err)
		}
		return nil
	}
	if err := os.Rename(src, target); err == nil {
		return nil
	}
	return copyReplace(src, target)
}

// installReplace places the binary at src into dst without consuming src
// (unlike replaceBinary, which moves src). It stages a copy in dst's directory,
// moves any existing dst aside to dst+".old" — a rename succeeds even when the
// destination cannot be overwritten in place — then renames the staged copy
// into position. Used by the Windows self-installer, where src is the
// downloaded exe the user may want to keep, and the old install may be locked.
func installReplace(src, dst string) error {
	staged, err := os.CreateTemp(filepath.Dir(dst), ".ops-install-*")
	if err != nil {
		return err
	}
	stagedPath := staged.Name()
	defer os.Remove(stagedPath)

	sf, err := os.Open(src)
	if err != nil {
		staged.Close()
		return err
	}
	_, copyErr := io.Copy(staged, sf)
	sf.Close()
	staged.Close()
	if copyErr != nil {
		return copyErr
	}
	if err := os.Chmod(stagedPath, 0o755); err != nil {
		return err
	}

	if _, err := os.Stat(dst); err == nil {
		old := dst + ".old"
		_ = os.Remove(old) // clear any leftover from a previous install
		if err := os.Rename(dst, old); err != nil {
			return fmt.Errorf("move existing binary aside: %w", err)
		}
		defer os.Remove(old) // best-effort cleanup once the new one is in place
	}
	if err := os.Rename(stagedPath, dst); err != nil {
		return fmt.Errorf("place new binary: %w", err)
	}
	return nil
}

// copyReplace copies src to a temp file in the same directory as dst, then
// renames it into place. Used when src and dst are on different filesystems.
func copyReplace(src, dst string) error {
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".ops-update-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	sf, err := os.Open(src)
	if err != nil {
		tmp.Close()
		return err
	}
	defer sf.Close()

	if _, err := io.Copy(tmp, sf); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()

	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

// suggestServiceRestart prints a reminder to restart the systemd service if it
// is currently active. Called only on Linux; silently does nothing when
// systemctl is absent or the service is not running.
func suggestServiceRestart() {
	out, err := exec.Command("systemctl", "is-active", "opsagent").Output()
	if err != nil || strings.TrimSpace(string(out)) != "active" {
		return
	}
	fmt.Println("\nopsagent service is running — restart to apply the update:")
	fmt.Println("  sudo systemctl restart opsagent")
}
