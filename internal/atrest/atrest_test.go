// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package atrest

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	keyMu.Lock()
	cached, lockedUntil = nil, time.Time{}
	keyMu.Unlock()
	t.Cleanup(func() {
		keyMu.Lock()
		cached, lockedUntil = nil, time.Time{}
		keyMu.Unlock()
		osStoreFor, allowOSStore = osStore, osStoreAllowed
	})
	return filepath.Join(home, ".reminal")
}

func resetCache() {
	keyMu.Lock()
	cached, lockedUntil = nil, time.Time{}
	keyMu.Unlock()
}

// fakeStore is an OS keystore whose answers the test controls.
type fakeStore struct {
	mu     sync.Mutex
	key    []byte
	locked bool
	puts   int
}

func (*fakeStore) name() string { return "keychain" }
func (*fakeStore) source() byte { return srcKeychain }
func (f *fakeStore) get() ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.locked {
		return nil, ErrLocked
	}
	if f.key == nil {
		return nil, errNotFound
	}
	return append([]byte(nil), f.key...), nil
}
func (f *fakeStore) put(k []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.locked {
		return ErrLocked
	}
	f.puts++
	f.key = append([]byte(nil), k...)
	return nil
}

func useFake(f *fakeStore) {
	osStoreFor = func(string) store { return f }
	allowOSStore = func() bool { return true }
}

func TestSealOpenRoundTripAndBinding(t *testing.T) {
	dir := isolate(t)
	blob, err := Seal("restore", "ABCD2345", []byte("pin 424242"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, []byte("424242")) {
		t.Fatal("sealed blob holds the plaintext")
	}
	if Backend() != "file" {
		t.Fatalf("backend in a test = %q, want file", Backend())
	}
	if fi, err := os.Stat(filepath.Join(dir, "atrest.key")); err != nil {
		t.Fatal(err)
	} else if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode %o", fi.Mode().Perm())
	}
	pt, err := Open("restore", "ABCD2345", blob)
	if err != nil || string(pt) != "pin 424242" {
		t.Fatalf("open = %q, %v", pt, err)
	}
	if _, err := Open("restore", "OTHER234", blob); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("another session's id: %v, want ErrCorrupt", err)
	}
	if _, err := Open("scrollback", "ABCD2345", blob); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("another kind: %v, want ErrCorrupt", err)
	}
	bad := append([]byte(nil), blob...)
	bad[len(bad)-1] ^= 1
	if _, err := Open("restore", "ABCD2345", bad); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("tampered: %v, want ErrCorrupt", err)
	}
	if _, err := Open("restore", "ABCD2345", []byte(`{"pin":"1"}`)); !errors.Is(err, ErrNotSealed) {
		t.Fatalf("plain: %v, want ErrNotSealed", err)
	}
}

// The key file deleted after a seal: the store answered "none", so the blob
// is gone for good (ErrKeyGone) — and the next Seal makes a new key rather
// than failing forever.
func TestKeyFileRemovedIsKeyGone(t *testing.T) {
	dir := isolate(t)
	blob, err := Seal("restore", "ABCD2345", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "atrest.key")); err != nil {
		t.Fatal(err)
	}
	resetCache()
	if _, err := Open("restore", "ABCD2345", blob); !errors.Is(err, ErrKeyGone) {
		t.Fatalf("open after key removed: %v, want ErrKeyGone", err)
	}
	blob2, err := Seal("restore", "ABCD2345", []byte("y"))
	if err != nil {
		t.Fatalf("seal after key removed: %v", err)
	}
	if pt, err := Open("restore", "ABCD2345", blob2); err != nil || string(pt) != "y" {
		t.Fatalf("new key: %q %v", pt, err)
	}
	if _, err := Open("restore", "ABCD2345", blob); !errors.Is(err, ErrKeyGone) {
		t.Fatalf("old blob under new key: %v, want ErrKeyGone", err)
	}
}

// A locked keystore is never a lost key: Open and Seal say "later", no new
// key is made, and the metadata is untouched.
func TestLockedKeystoreNeverMints(t *testing.T) {
	dir := isolate(t)
	f := &fakeStore{}
	useFake(f)
	blob, err := Seal("restore", "ABCD2345", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if Backend() != "keychain" || f.puts != 1 {
		t.Fatalf("backend %q puts %d", Backend(), f.puts)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "atrest.json"))
	f.locked = true
	resetCache()
	if _, err := Open("restore", "ABCD2345", blob); !errors.Is(err, ErrLocked) {
		t.Fatalf("open while locked: %v", err)
	}
	resetCache()
	if _, err := Seal("restore", "ABCD2345", []byte("y")); !errors.Is(err, ErrLocked) {
		t.Fatalf("seal while locked: %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "atrest.json"))
	if !bytes.Equal(before, after) || f.puts != 1 {
		t.Fatal("a locked keystore led to a new key")
	}
	if _, err := os.Stat(filepath.Join(dir, "atrest.key")); err == nil {
		t.Fatal("a locked keystore fell back to a key file")
	}
	f.locked = false
	resetCache()
	if pt, err := Open("restore", "ABCD2345", blob); err != nil || string(pt) != "x" {
		t.Fatalf("after unlock: %q %v", pt, err)
	}
}

// Lost metadata: the key the keystore still holds is adopted, not replaced.
func TestLostMetadataAdoptsStoredKey(t *testing.T) {
	dir := isolate(t)
	f := &fakeStore{}
	useFake(f)
	blob, _ := Seal("restore", "ABCD2345", []byte("x"))
	_ = os.Remove(filepath.Join(dir, "atrest.json"))
	resetCache()
	if pt, err := Open("restore", "ABCD2345", blob); err != nil || string(pt) != "x" {
		t.Fatalf("open with lost metadata: %q %v", pt, err)
	}
	if _, err := Seal("restore", "ABCD2345", []byte("y")); err != nil || f.puts != 1 {
		t.Fatalf("seal re-minted: puts %d err %v", f.puts, err)
	}
}

// No OS keystore available at first start: the file, and the session goes on.
func TestNoKeystoreFallsBackToFile(t *testing.T) {
	isolate(t)
	f := &fakeStore{locked: true}
	useFake(f)
	if _, err := Seal("restore", "ABCD2345", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if Backend() != "file" {
		t.Fatalf("backend %q, want file", Backend())
	}
}

// Many sessions starting at once agree on one key.
func TestConcurrentFirstSealOneKey(t *testing.T) {
	isolate(t)
	var wg sync.WaitGroup
	blobs := make([][]byte, 16)
	for i := range blobs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			blobs[i], _ = Seal("restore", "ABCD2345", []byte("x"))
		}(i)
	}
	wg.Wait()
	resetCache()
	for i, b := range blobs {
		if _, err := Open("restore", "ABCD2345", b); err != nil {
			t.Fatalf("blob %d: %v", i, err)
		}
	}
}

func TestSealWithOneTimeKey(t *testing.T) {
	k, _ := NewKey()
	b, err := SealWith(k, "handoff", "ABCD2345", []byte("history"))
	if err != nil {
		t.Fatal(err)
	}
	if pt, err := OpenWith(k, "handoff", "ABCD2345", b); err != nil || string(pt) != "history" {
		t.Fatalf("%q %v", pt, err)
	}
	k2, _ := NewKey()
	if _, err := OpenWith(k2, "handoff", "ABCD2345", b); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("wrong key: %v", err)
	}
}

// A keystore helper that hangs (secret-tool waiting on a D-Bus that is not
// there) is cut off at the timeout and counts as locked.
func TestHangingHelperTimesOut(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script")
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	old := keystoreTimeout
	keystoreTimeout = 300 * time.Millisecond
	defer func() { keystoreTimeout = old }()
	start := time.Now()
	_, _, _, err = runTool(nil, sh, "-c", "sleep 30")
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("hang: %v, want ErrLocked", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("hang took %v", d)
	}
	// A helper that forks a child holding our pipes is cut off too.
	start = time.Now()
	_, _, _, err = runTool(nil, sh, "-c", "(sleep 30 &) ; sleep 30")
	if !errors.Is(err, ErrLocked) || time.Since(start) > 2*time.Second {
		t.Fatalf("forked hang: %v after %v", err, time.Since(start))
	}
}

func TestQuarantineKeepsAndPrunes(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "ABCD2345.sealed")
	_ = os.WriteFile(f, []byte("blob"), 0o600)
	if err := Quarantine(dir, "ABCD2345", "session ABCD2345: key gone", f); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f); !os.IsNotExist(err) {
		t.Fatal("file not moved")
	}
	ents, _ := os.ReadDir(filepath.Join(dir, "quarantine"))
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	if len(ents) != 2 || !strings.Contains(strings.Join(names, " "), ".reason") {
		t.Fatalf("quarantine holds %v", names)
	}
	if n := PruneQuarantine(dir); n != 1 {
		t.Fatalf("held = %d, want 1", n)
	}
	old := time.Now().Add(-QuarantineKeep - time.Hour)
	for _, e := range ents {
		_ = os.Chtimes(filepath.Join(dir, "quarantine", e.Name()), old, old)
	}
	if n := PruneQuarantine(dir); n != 0 {
		t.Fatalf("after keep: held %d", n)
	}
	if ents, _ := os.ReadDir(filepath.Join(dir, "quarantine")); len(ents) != 0 {
		t.Fatal("old quarantine not pruned")
	}
}
