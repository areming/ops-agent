package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"
)

// releaseRepo is the public GitHub repo whose releases host the agent
// binaries. It is hardcoded so the common path needs zero configuration.
const releaseRepo = "areming/ops-agent"

// releaseAsset is the release asset name for a Go arch (amd64|arm64).
// Used by enroll which always targets Linux.
func releaseAsset(arch string) string { return "ops-linux-" + arch }

// releaseBinAsset returns the asset filename for any supported OS/arch pair.
func releaseBinAsset(goos, arch string) string {
	name := "ops-" + goos + "-" + arch
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// releaseBinURL is the download URL for the agent binary of a given version
// and arch. Targets Linux; used by enroll.
func releaseBinURL(version, arch string) string {
	return fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", releaseRepo, version, releaseAsset(arch))
}

// releaseBinURLForPlatform returns the download URL for any OS/arch pair.
func releaseBinURLForPlatform(ver, goos, arch string) string {
	return fmt.Sprintf("https://github.com/%s/releases/download/%s/%s",
		releaseRepo, ver, releaseBinAsset(goos, arch))
}

// LatestReleaseVersion queries the GitHub releases API and returns the tag
// name of the latest published release (e.g. "v0.5.0").
func LatestReleaseVersion() (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", releaseRepo)
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API: %s", resp.Status)
	}
	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&payload); err != nil {
		return "", err
	}
	if payload.TagName == "" {
		return "", fmt.Errorf("empty tag_name in GitHub response")
	}
	return payload.TagName, nil
}

// releaseSumsURL is the download URL for the release's SHA256SUMS file.
func releaseSumsURL(version string) string {
	return fmt.Sprintf("https://github.com/%s/releases/download/%s/SHA256SUMS", releaseRepo, version)
}

// fetchChecksum downloads SHA256SUMS for the release and returns the hex
// digest of the linux/arch binary. The checksum is fetched locally over HTTPS
// and handed to the remote, which verifies its own download against it — so a
// tampered binary is rejected before it is installed and run as root.
func fetchChecksum(version, arch string) (string, error) {
	body, err := httpGetString(releaseSumsURL(version))
	if err != nil {
		return "", err
	}
	return parseChecksum(body, releaseAsset(arch))
}

// parseChecksum finds the digest for asset in `sha256sum`-style content
// (lines of "<hex>  <filename>").
func parseChecksum(sums, asset string) (string, error) {
	for line := range strings.SplitSeq(sums, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && path.Base(fields[1]) == asset {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("no checksum for %s in SHA256SUMS", asset)
}

// httpGetString fetches url and returns its body, failing on non-200.
func httpGetString(url string) (string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	return string(b), nil
}
