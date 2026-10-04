// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package atrest

import (
	"bytes"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"reminal/internal/atomicfile"
)

func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	ResetCacheForTest()
	t.Cleanup(func() {
		ResetCacheForTest()
		osStoreFor, osUsable = osStore, osStoreUsable
		allowOSStore = func() bool { return osUsable() && os.Getenv("REMINAL_KEYSTORE") != "file" }
	})
	return filepath.Join(home, ".reminal")
}

func resetCache() { ResetCacheForTest() }

// fakeStore is an OS keystore whose answers the test controls. Entries are
// per account, like a real keychain; key is the main account's entry.
type fakeStore struct {
	mu      sync.Mutex
	key     []byte
	entries map[string][]byte
	locked  bool
	puts    int
}

type fakeEntry struct {
	f    *fakeStore
	acct string
}

func (fakeEntry) name() string { return "keychain" }
func (fakeEntry) source() byte { return srcKeychain }

func (e fakeEntry) main() bool { return !strings.Contains(e.acct, ".") }

func (e fakeEntry) get() ([]byte, error) {
	e.f.mu.Lock()
	defer e.f.mu.Unlock()
	if e.f.locked {
		return nil, ErrLocked
	}
	k := e.f.entries[e.acct]
	if e.main() {
		k = e.f.key
	}
	if k == nil {
		return nil, errNotFound
	}
	return append([]byte(nil), k...), nil
}

func (e fakeEntry) put(k []byte) error {
	e.f.mu.Lock()
	defer e.f.mu.Unlock()
	if e.f.locked {
		return ErrLocked
	}
	e.f.puts++
	if e.main() {
		e.f.key = append([]byte(nil), k...)
		return nil
	}
	if e.f.entries == nil {
		e.f.entries = map[string][]byte{}
	}
	e.f.entries[e.acct] = append([]byte(nil), k...)
	return nil
}

func useFake(f *fakeStore) {
	osStoreFor = func(_ string, acct string) store { return fakeEntry{f, acct} }
	osUsable = func() bool { return true }
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

// A locked keystore is never a lost key: nothing sealed under it is given
// up, no replacement is minted, atrest.json is untouched — and saving goes on
// with the key file meanwhile, so a session's details are not lost either.
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
	fb, err := Seal("restore", "ABCD2345", []byte("y"))
	if err != nil {
		t.Fatalf("seal while locked: %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "atrest.json"))
	if !bytes.Equal(before, after) || f.puts != 1 {
		t.Fatal("a locked keystore led to a new key")
	}
	if _, err := os.Stat(filepath.Join(dir, "atrest.key")); err != nil {
		t.Fatal("no file fallback while locked")
	}
	f.locked = false
	resetCache()
	if pt, err := Open("restore", "ABCD2345", blob); err != nil || string(pt) != "x" {
		t.Fatalf("keychain blob after unlock: %q %v", pt, err)
	}
	if pt, err := Open("restore", "ABCD2345", fb); err != nil || string(pt) != "y" {
		t.Fatalf("fallback blob after unlock: %q %v", pt, err)
	}
	// Back to the keychain key once it answers again.
	nb, _ := Seal("restore", "ABCD2345", []byte("z"))
	if nb[len(magic)+1] != srcKeychain {
		t.Fatalf("after unlock sealed with source %q", nb[len(magic)+1])
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

// No OS keystore answering at first start: the file, the session goes on,
// and once the keystore answers the file's key moves into it (the same key,
// so everything sealed meanwhile still opens) and the file goes.
func TestNoKeystoreFallsBackToFile(t *testing.T) {
	dir := isolate(t)
	f := &fakeStore{locked: true}
	useFake(f)
	b1, err := Seal("restore", "ABCD2345", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	f.locked = false
	resetCache()
	b2, err := Seal("restore", "ABCD2345", []byte("y"))
	if err != nil {
		t.Fatal(err)
	}
	if Backend() != "keychain" || f.puts != 1 {
		t.Fatalf("backend %q puts %d, want the file key moved to the keystore", Backend(), f.puts)
	}
	if _, err := os.Stat(filepath.Join(dir, "atrest.key")); !os.IsNotExist(err) {
		t.Fatal("key file left behind after moving into the keystore")
	}
	resetCache()
	for _, b := range [][]byte{b1, b2} {
		if _, err := Open("restore", "ABCD2345", b); err != nil {
			t.Fatal(err)
		}
	}
}

// A blob opens by whichever store holds its key: atrest.json lost while the
// key file holds the key, and an unrelated key sitting in the keystore, must
// not get the blob quarantined.
func TestOpenFindsKeyByID(t *testing.T) {
	dir := isolate(t)
	blob, _ := Seal("restore", "ABCD2345", []byte("x")) // file key (tests never use the OS store)
	_ = os.Remove(filepath.Join(dir, "atrest.json"))
	other, _ := NewKey()
	f := &fakeStore{key: other}
	useFake(f)
	resetCache()
	if pt, err := Open("restore", "ABCD2345", blob); err != nil || string(pt) != "x" {
		t.Fatalf("open with the key in the file, another in the keystore: %q %v", pt, err)
	}
	// With the keystore locked and the blob's key nowhere else: later, not gone.
	_ = os.Remove(filepath.Join(dir, "atrest.key"))
	f.locked = true
	resetCache()
	if _, err := Open("restore", "ABCD2345", blob); !errors.Is(err, ErrLocked) {
		t.Fatalf("key nowhere reachable, keystore locked: %v, want ErrLocked", err)
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
	// Last saved long ago: the 7 days still count from the quarantine.
	long := time.Now().Add(-30 * 24 * time.Hour)
	_ = os.Chtimes(f, long, long)
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

// atrest.json naming a different key than the keystore holds (an old
// ~/.reminal restored from a backup): the keystore's key is kept, never
// written over.
func TestStaleMetadataNeverOverwritesStoredKey(t *testing.T) {
	dir := isolate(t)
	f := &fakeStore{}
	useFake(f)
	blob, _ := Seal("restore", "ABCD2345", []byte("x"))
	held := append([]byte(nil), f.key...)
	other, _ := NewKey()
	oid := keyID(other)
	_ = writeMeta(dir, meta{V: 1, Source: "keychain", ID: hex.EncodeToString(oid[:]), Account: "acct"})
	resetCache()
	if _, err := Seal("restore", "ABCD2345", []byte("y")); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(f.key, held) || f.puts != 1 {
		t.Fatal("the key the keystore held was written over")
	}
	resetCache()
	if pt, err := Open("restore", "ABCD2345", blob); err != nil || string(pt) != "x" {
		t.Fatalf("%q %v", pt, err)
	}
}

// atrest.json damaged or deleted while the keystore that holds the key cannot
// be reached (no session bus): later, never gone, and no key is made over it.
func TestDamagedMetaWithUnreachableStoreIsLocked(t *testing.T) {
	for _, damage := range []string{"corrupt", "deleted"} {
		t.Run(damage, func(t *testing.T) {
			dir := isolate(t)
			f := &fakeStore{}
			useFake(f)
			blob, err := Seal("restore", "ABCD2345", []byte("x"))
			if err != nil || blob[len(magic)+1] != srcKeychain {
				t.Fatalf("seal: %v", err)
			}
			if damage == "corrupt" {
				_ = os.WriteFile(filepath.Join(dir, "atrest.json"), []byte("{not json"), 0o600)
			} else {
				_ = os.Remove(filepath.Join(dir, "atrest.json"))
			}
			osStoreFor = func(string, string) store { return nil } // unreachable from here
			resetCache()
			if _, err := Open("restore", "ABCD2345", blob); !errors.Is(err, ErrLocked) {
				t.Fatalf("open: %v, want ErrLocked", err)
			}
			if damage == "corrupt" {
				// Saving goes on with the key file; atrest.json is left alone.
				before, _ := os.ReadFile(filepath.Join(dir, "atrest.json"))
				if _, err := Seal("restore", "ABCD2345", []byte("y")); err != nil {
					t.Fatalf("seal with damaged meta: %v", err)
				}
				after, _ := os.ReadFile(filepath.Join(dir, "atrest.json"))
				if !bytes.Equal(before, after) {
					t.Fatal("a damaged atrest.json was rewritten")
				}
			}
			// Reachable again: the blob opens.
			useFake(f)
			resetCache()
			if pt, err := Open("restore", "ABCD2345", blob); err != nil || string(pt) != "x" {
				t.Fatalf("after: %q %v", pt, err)
			}
		})
	}
}

// An empty (or garbled) key file — a crash mid-write — is never "no key":
// blobs are locked, not gone, and the file is never written over.
func TestEmptyKeyFileIsLockedNeverReplaced(t *testing.T) {
	for _, content := range []string{"", "zz-not-hex\n"} {
		dir := isolate(t)
		blob, err := Seal("restore", "ABCD2345", []byte("x"))
		if err != nil {
			t.Fatal(err)
		}
		kf := filepath.Join(dir, "atrest.key")
		good, _ := os.ReadFile(kf)
		_ = os.WriteFile(kf, []byte(content), 0o600)
		resetCache()
		if _, err := Open("restore", "ABCD2345", blob); !errors.Is(err, ErrLocked) {
			t.Fatalf("%q: open %v, want ErrLocked", content, err)
		}
		resetCache()
		if _, err := Seal("restore", "ABCD2345", []byte("y")); err == nil {
			t.Fatalf("%q: sealed with a key file it cannot read", content)
		}
		if b, _ := os.ReadFile(kf); string(b) != content {
			t.Fatalf("%q: key file was written over", content)
		}
		_ = os.WriteFile(kf, good, 0o600)
		resetCache()
		if pt, err := Open("restore", "ABCD2345", blob); err != nil || string(pt) != "x" {
			t.Fatalf("%q: after repair %q %v", content, pt, err)
		}
	}
}

// A second key-file writer never replaces the first one's file.
func TestKeyFileNeverReplaced(t *testing.T) {
	dir := isolate(t)
	fs := fileStore{dir: dir}
	_ = os.MkdirAll(dir, 0o700)
	a, _ := NewKey()
	b, _ := NewKey()
	if err := fs.put(a); err != nil {
		t.Fatal(err)
	}
	if err := fs.put(b); err == nil {
		t.Fatal("second put replaced the key file")
	}
	if got, _ := fs.get(); !bytes.Equal(got, a) {
		t.Fatal("key file changed")
	}
}

// A key file made while the keychain was locked moves into the keychain (by
// id) once it answers, and the file goes; what it sealed still opens.
func TestFallbackKeyMovesIntoKeystore(t *testing.T) {
	dir := isolate(t)
	f := &fakeStore{}
	useFake(f)
	if _, err := Seal("restore", "ABCD2345", []byte("x")); err != nil {
		t.Fatal(err)
	}
	f.locked = true
	resetCache()
	fb, err := Seal("restore", "ABCD2345", []byte("while locked"))
	if err != nil || fb[len(magic)+1] != srcFile {
		t.Fatalf("fallback seal: %v", err)
	}
	f.locked = false
	resetCache()
	if _, err := Seal("restore", "ABCD2345", []byte("after")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "atrest.key")); !os.IsNotExist(err) {
		t.Fatal("key file still there after the keystore answered")
	}
	if len(f.entries) != 1 {
		t.Fatalf("keystore entries by id: %d", len(f.entries))
	}
	resetCache()
	if pt, err := Open("restore", "ABCD2345", fb); err != nil || string(pt) != "while locked" {
		t.Fatalf("fallback blob after the move: %q %v", pt, err)
	}
}

// After a key file has moved into the keystore, a run that cannot reach the
// keystore (no session bus, a boot-time daemon, a different HOME) must call
// the blobs it sealed "later", never "gone" — on both move paths, and with
// atrest.json deleted too.
func TestMovedKeyThenUnreachableStoreIsLocked(t *testing.T) {
	unreachable := func() {
		osStoreFor = func(string, string) store { return nil }
		resetCache()
	}
	t.Run("moved while atrest.json named the keystore", func(t *testing.T) {
		dir := isolate(t)
		f := &fakeStore{}
		useFake(f)
		_, _ = Seal("restore", "ABCD2345", []byte("x"))
		f.locked = true
		resetCache()
		fb, _ := Seal("restore", "ABCD2345", []byte("while locked")) // file key
		f.locked = false
		resetCache()
		_, _ = Seal("restore", "ABCD2345", []byte("after")) // moves the file key
		if _, err := os.Stat(filepath.Join(dir, "atrest.key")); !os.IsNotExist(err) {
			t.Fatal("key file not moved")
		}
		unreachable()
		if _, err := Open("restore", "ABCD2345", fb); !errors.Is(err, ErrLocked) {
			t.Fatalf("got %v, want ErrLocked", err)
		}
	})
	t.Run("moved at a first start that had fallen back", func(t *testing.T) {
		dir := isolate(t)
		f := &fakeStore{locked: true}
		useFake(f)
		fb, _ := Seal("restore", "ABCD2345", []byte("first start")) // file key, no atrest.json
		f.locked = false
		resetCache()
		_, _ = Seal("restore", "ABCD2345", []byte("after")) // promoteFileKey
		if Backend() != "keychain" {
			t.Fatalf("backend %q", Backend())
		}
		unreachable()
		if _, err := Open("restore", "ABCD2345", fb); !errors.Is(err, ErrLocked) {
			t.Fatalf("got %v, want ErrLocked", err)
		}
		// ...and with atrest.json deleted as well.
		_ = os.Remove(filepath.Join(dir, "atrest.json"))
		resetCache()
		if _, err := Open("restore", "ABCD2345", fb); !errors.Is(err, ErrLocked) {
			t.Fatalf("meta deleted: got %v, want ErrLocked", err)
		}
		// Reachable again: it opens.
		useFake(f)
		resetCache()
		if pt, err := Open("restore", "ABCD2345", fb); err != nil || string(pt) != "first start" {
			t.Fatalf("reachable again: %q %v", pt, err)
		}
	})
}

// No hard links (FAT, some network shares): the key file is still written,
// exclusively, and still never replaced.
func TestWriteExclusiveFallback(t *testing.T) {
	p := filepath.Join(t.TempDir(), "k")
	if err := atomicfile.WriteNewNoLinkForTest(p, []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := atomicfile.WriteNewNoLinkForTest(p, []byte("b"), 0o600); !errors.Is(err, atomicfile.ErrExists) {
		t.Fatalf("second write: %v", err)
	}
	if b, _ := os.ReadFile(p); string(b) != "a" {
		t.Fatal("replaced")
	}
}
