package cli

import (
	"strings"
	"testing"
)

func TestReleaseURLs(t *testing.T) {
	bin := releaseBinURL("v1.2.3", "arm64")
	if !strings.HasSuffix(bin, "/releases/download/v1.2.3/ops-linux-arm64") {
		t.Errorf("bin URL = %q", bin)
	}
	sums := releaseSumsURL("v1.2.3")
	if !strings.HasSuffix(sums, "/releases/download/v1.2.3/SHA256SUMS") {
		t.Errorf("sums URL = %q", sums)
	}
}

func TestParseChecksum(t *testing.T) {
	sums := "aaa111  ops-linux-amd64\nbbb222  ops-linux-arm64\n"

	got, err := parseChecksum(sums, "ops-linux-arm64")
	if err != nil {
		t.Fatalf("parseChecksum: %v", err)
	}
	if got != "bbb222" {
		t.Errorf("digest = %q, want bbb222", got)
	}

	if _, err := parseChecksum(sums, "ops-linux-riscv64"); err == nil {
		t.Error("missing asset should error")
	}
}
