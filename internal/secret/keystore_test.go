package secret

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func paths(t *testing.T) (store, master string) {
	t.Helper()
	dir := t.TempDir()
	return filepath.Join(dir, "keystore.json"), filepath.Join(dir, "master.key")
}

func TestSetGetRoundTrip(t *testing.T) {
	store, master := paths(t)
	ks, err := Open(store, master)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := ks.Set("api_key", "sk-secret-123"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, ok, err := ks.Get("api_key")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got != "sk-secret-123" {
		t.Errorf("Get = %q, want %q", got, "sk-secret-123")
	}

	if _, ok, _ := ks.Get("missing"); ok {
		t.Errorf("Get(missing) returned ok=true")
	}
}

func TestReopenStillDecrypts(t *testing.T) {
	store, master := paths(t)
	ks, err := Open(store, master)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := ks.Set("api_key", "value-survives-restart"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Simulate a restart: a fresh process re-reads the same master key file
	// and ciphertext store, with no human in the loop.
	reopened, err := Open(store, master)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, ok, err := reopened.Get("api_key")
	if err != nil || !ok {
		t.Fatalf("Get after reopen: ok=%v err=%v", ok, err)
	}
	if got != "value-survives-restart" {
		t.Errorf("Get after reopen = %q", got)
	}
}

func TestListNamesNotValues(t *testing.T) {
	store, master := paths(t)
	ks, _ := Open(store, master)
	_ = ks.Set("beta", "v2")
	_ = ks.Set("alpha", "v1")

	names := ks.List()
	if len(names) != 2 || names[0] != "alpha" || names[1] != "beta" {
		t.Fatalf("List = %v, want sorted [alpha beta]", names)
	}

	// The on-disk store must not contain the plaintext values.
	raw, err := os.ReadFile(store)
	if err != nil {
		t.Fatalf("read store: %v", err)
	}
	if strings.Contains(string(raw), "v1") || strings.Contains(string(raw), "v2") {
		t.Errorf("plaintext value found in store file: %s", raw)
	}
}

func TestTamperFailsDecryption(t *testing.T) {
	store, master := paths(t)
	ks, _ := Open(store, master)
	_ = ks.Set("api_key", "do-not-tamper")

	e := ks.entries["api_key"]
	e.Box[0] ^= 0xff // flip a ciphertext bit
	ks.entries["api_key"] = e

	if _, _, err := ks.Get("api_key"); err == nil {
		t.Error("Get on tampered ciphertext succeeded, want authentication failure")
	}
}

func TestWrongMasterKeyFails(t *testing.T) {
	store, master := paths(t)
	ks, _ := Open(store, master)
	_ = ks.Set("api_key", "sealed")

	// A different master key (separate file) must not open the store.
	_, other := paths(t)
	wrong, err := Open(store, other)
	if err != nil {
		t.Fatalf("Open with other master: %v", err)
	}
	if _, _, err := wrong.Get("api_key"); err == nil {
		t.Error("Get with wrong master key succeeded, want failure")
	}
}

func TestFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix file permissions not enforced on windows")
	}
	store, master := paths(t)
	ks, _ := Open(store, master)
	_ = ks.Set("api_key", "v")

	for _, p := range []string{store, master} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s perm = %o, want 600", p, perm)
		}
	}
}
