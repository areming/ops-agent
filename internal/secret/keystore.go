// Package secret stores API keys and other secrets encrypted at rest, so
// the agent can recover them unattended after a restart without a plaintext
// key sitting in config, environment, or the process list.
//
// Threat model: a random 32-byte master key lives in its own 0600 file,
// separate from the ciphertext store. This protects against config/env/
// backup leakage and casual disk reads. It does NOT protect against an
// attacker who can already read the agent user's files — unattended
// self-decryption inherently requires the key to be reachable without a
// human, so that trade-off is deliberate.
package secret

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"golang.org/x/crypto/nacl/secretbox"
)

const (
	keyLen   = 32 // secretbox key size
	nonceLen = 24 // secretbox nonce size
)

// entry is one sealed secret: a per-secret random nonce plus the box.
type entry struct {
	Nonce []byte `json:"nonce"`
	Box   []byte `json:"box"`
}

// Keystore holds secrets sealed under a master key. It is not safe for
// concurrent use; the agent opens it briefly at startup and the `key`
// subcommand opens it in a separate process.
type Keystore struct {
	path    string
	master  [keyLen]byte
	entries map[string]entry
}

// Open loads (creating if absent) the master key and the ciphertext store.
// The master key file is generated on first use with 0600 permissions.
func Open(storePath, masterKeyPath string) (*Keystore, error) {
	master, err := loadOrCreateMasterKey(masterKeyPath)
	if err != nil {
		return nil, err
	}
	ks := &Keystore{path: storePath, master: master, entries: map[string]entry{}}
	if err := ks.load(); err != nil {
		return nil, err
	}
	return ks, nil
}

// Set seals value under name and persists the store atomically.
func (k *Keystore) Set(name, value string) error {
	var nonce [nonceLen]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	box := secretbox.Seal(nil, []byte(value), &nonce, &k.master)
	k.entries[name] = entry{Nonce: nonce[:], Box: box}
	return k.save()
}

// Delete removes the secret stored under name and persists the store. Removing
// a missing entry is a no-op (no error), so deleting a profile whose key was
// never sealed still succeeds.
func (k *Keystore) Delete(name string) error {
	if _, ok := k.entries[name]; !ok {
		return nil
	}
	delete(k.entries, name)
	return k.save()
}

// Get opens the secret stored under name. ok is false if there is no such
// entry; an error means the ciphertext failed to authenticate (tampering or
// a mismatched master key).
func (k *Keystore) Get(name string) (string, bool, error) {
	e, ok := k.entries[name]
	if !ok {
		return "", false, nil
	}
	if len(e.Nonce) != nonceLen {
		return "", true, fmt.Errorf("secret %q: malformed nonce", name)
	}
	var nonce [nonceLen]byte
	copy(nonce[:], e.Nonce)
	plain, ok := secretbox.Open(nil, e.Box, &nonce, &k.master)
	if !ok {
		return "", true, fmt.Errorf("secret %q: decryption failed", name)
	}
	return string(plain), true, nil
}

// List returns the secret names in sorted order. Values are never returned.
func (k *Keystore) List() []string {
	names := make([]string, 0, len(k.entries))
	for n := range k.entries {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func (k *Keystore) load() error {
	b, err := os.ReadFile(k.path)
	if os.IsNotExist(err) {
		return nil // empty store until first Set
	}
	if err != nil {
		return err
	}
	if len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, &k.entries)
}

func (k *Keystore) save() error {
	b, err := json.Marshal(k.entries)
	if err != nil {
		return err
	}
	return writeFileAtomic(k.path, b, 0o600)
}

func loadOrCreateMasterKey(path string) ([keyLen]byte, error) {
	var key [keyLen]byte
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		if len(b) != keyLen {
			return key, fmt.Errorf("master key %s: want %d bytes, got %d", path, keyLen, len(b))
		}
		copy(key[:], b)
		return key, nil
	case os.IsNotExist(err):
		if _, err := rand.Read(key[:]); err != nil {
			return key, err
		}
		if err := writeFileAtomic(path, key[:], 0o600); err != nil {
			return key, err
		}
		return key, nil
	default:
		return key, err
	}
}

// writeFileAtomic writes via a temp file in the same directory then renames,
// so a crash mid-write can never leave a half-written secret store.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once renamed
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
