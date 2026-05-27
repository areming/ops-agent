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
