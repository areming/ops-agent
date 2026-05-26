package cli

import (
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
func releaseAsset(arch string) string { return "ops-linux-" + arch }

// releaseBinURL is the download URL for the agent binary of a given version
// and arch.
func releaseBinURL(version, arch string) string {
	return fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", releaseRepo, version, releaseAsset(arch))
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
