package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestReleaseBinAsset(t *testing.T) {
	cases := []struct{ goos, arch, want string }{
		{"linux", "amd64", "ops-linux-amd64"},
		{"linux", "arm64", "ops-linux-arm64"},
		{"windows", "amd64", "ops-windows-amd64.exe"},
		{"darwin", "arm64", "ops-darwin-arm64"},
	}
	for _, tc := range cases {
		if got := releaseBinAsset(tc.goos, tc.arch); got != tc.want {
			t.Errorf("releaseBinAsset(%q,%q) = %q, want %q", tc.goos, tc.arch, got, tc.want)
		}
	}
}

func TestShouldOfferUpdate(t *testing.T) {
	cases := []struct {
		remote, latest string
		want           bool
	}{
		{"v0.0.2", "v0.0.3", true},  // older remote → offer
		{"v0.0.3", "v0.0.3", false}, // already current → no offer
		{"v0.0.4", "v0.0.3", true},  // differs (newer remote) → still offer; user decides
		{"dev", "v0.0.3", false},    // unversioned local build → can't compare, skip
		{"", "v0.0.3", false},       // version probe failed → skip
		{"v0.0.3", "", false},       // release lookup failed → skip
	}
	for _, tc := range cases {
		if got := shouldOfferUpdate(tc.remote, tc.latest); got != tc.want {
			t.Errorf("shouldOfferUpdate(%q,%q) = %v, want %v", tc.remote, tc.latest, got, tc.want)
		}
	}
}

func TestVerifyFile(t *testing.T) {
	content := []byte("hello ops update")
	sum := sha256.Sum256(content)
	want := hex.EncodeToString(sum[:])

	f, err := os.CreateTemp(t.TempDir(), "ops-verify-*")
	if err != nil {
		t.Fatal(err)
	}
	f.Write(content)
	f.Close()

	if err := verifyFile(f.Name(), want); err != nil {
		t.Fatalf("verifyFile with correct hash: %v", err)
	}
	if err := verifyFile(f.Name(), "deadbeef"); err == nil {
		t.Fatal("expected mismatch error for wrong hash")
	}
}

func TestInstallReplace(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "download", "ops.exe")
	dst := filepath.Join(dir, "install", "ops.exe")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("new binary v2"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Fresh install (no existing dst).
	if err := installReplace(src, dst); err != nil {
		t.Fatalf("installReplace (fresh): %v", err)
	}
	if got, _ := os.ReadFile(dst); string(got) != "new binary v2" {
		t.Fatalf("fresh install dst = %q, want new bytes", got)
	}

	// Update over an existing dst: it must be replaced, src kept, no .old left.
	if err := os.WriteFile(src, []byte("new binary v3"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := installReplace(src, dst); err != nil {
		t.Fatalf("installReplace (update): %v", err)
	}
	if got, _ := os.ReadFile(dst); string(got) != "new binary v3" {
		t.Fatalf("update dst = %q, want replaced bytes", got)
	}
	if _, err := os.Stat(src); err != nil {
		t.Errorf("src should remain after install, not be consumed: %v", err)
	}
	if _, err := os.Stat(dst + ".old"); !os.IsNotExist(err) {
		t.Errorf("dst.old leftover should be cleaned up after a successful install")
	}
}

func TestCopyReplace(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")

	if err := os.WriteFile(src, []byte("binary content"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyReplace(src, dst); err != nil {
		t.Fatalf("copyReplace: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "binary content" {
		t.Fatalf("unexpected content: %q", got)
	}
}
