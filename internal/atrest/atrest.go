// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

// Package atrest seals what reminal keeps on disk between runs — a session's
// PIN and relay token, and the scrollback a restore brings back — so a copy of
// ~/.reminal (a backup, a synced folder, a disk image) does not carry them in
// the clear.
//
// One random 32-byte key per user does the sealing. It lives in the keystore
// the OS offers — the login Keychain on macOS, DPAPI on Windows, the Secret
// Service on a Linux desktop — and in a 0600 file beside the data where there
// is none (a headless Linux box, a container, a run with HOME pointed
// elsewhere). The file fallback is no stronger than the plain files were; it
// only keeps the contents from being greppable.
//
// Every keystore call is bounded by a short timeout and a timeout counts as
// "locked": a missing D-Bus under systemd or SSH must never hang a session
// start. A locked keystore is never mistaken for a lost key — only a reachable
// keystore that says the key does not exist is (ErrKeyGone), and even then the
// caller quarantines what it cannot open rather than deleting it.
package atrest

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"reminal/internal/atomicfile"
)

var (
	// ErrLocked: the key exists but cannot be had right now — the keystore is
	// locked, unreachable or slow. Try again later; keep what is on disk.
	ErrLocked = errors.New("at-rest key is not available right now")
	// ErrKeyGone: the keystore answered and the key a blob was sealed with is
	// not there. What it sealed cannot be opened again; quarantine it.
	ErrKeyGone = errors.New("at-rest key is gone")
	// ErrCorrupt: the blob is damaged, or was sealed for another kind or id.
	ErrCorrupt = errors.New("sealed data is damaged")
	// ErrNotSealed: the bytes are not a sealed blob — a plain file written by
	// an older version.
	ErrNotSealed = errors.New("not sealed")
)

// Logf reports a one-line event (a fallback taken, a key minted). Callers
// that have somewhere to say it set this; by default it is silent.
var Logf = func(format string, args ...any) {}

// keystoreTimeout bounds every call into an OS keystore.
var keystoreTimeout = 3 * time.Second

const (
	magic   = "RMAR"
	version = 1
	keyLen  = 32
	idLen   = 8
	// header: magic | version | source | key id | nonce
	headerLen = len(magic) + 1 + 1 + idLen + 12
)

// Source bytes recorded in a blob's header — where its key lives.
const (
	srcFile      byte = 'f'
	srcKeychain  byte = 'k'
	srcDPAPI     byte = 'd'
	srcSecretSvc byte = 's'
	srcEphemeral byte = 'e'
)

// store is a place the key can live.
type store interface {
	name() string
	source() byte
	// get returns the key, errNotFound when the store answered that there is
	// none, or ErrLocked for anything else (a timeout included).
	get() ([]byte, error)
	put(key []byte) error
}

var errNotFound = errors.New("not found")

// Dir is ~/.reminal, where the key's metadata (and the file fallback) live.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".reminal"), nil
}

func metaPath(dir string) string { return filepath.Join(dir, "atrest.json") }

func keyID(k []byte) [idLen]byte {
	s := sha256.Sum256(append([]byte("reminal at-rest key id\x00"), k...))
	var id [idLen]byte
	copy(id[:], s[:idLen])
	return id
}

// ---- the key -------------------------------------------------------------------
//
// atrest.json names where the key lives (the store, and the account in an OS
// keystore) and its id. Blobs carry the id of the key that sealed them, so a
// blob is opened by whichever store holds that key: the one atrest.json names,
// the 0600 key file, or the OS keystore. A blob is ErrKeyGone only when every
// store that could hold its key answered and none did; any store that could
// not answer makes it ErrLocked instead.

type meta struct {
	V       int    `json:"v"`
	Source  string `json:"source"`
	ID      string `json:"id"`
	Account string `json:"account,omitempty"`
	// Promoted lists key ids moved from the key file into the OS keystore:
	// blobs sealed with them open only from the keystore now.
	Promoted []string `json:"promoted,omitempty"`
}

var (
	keyMu sync.Mutex
	// keys holds every key this process has seen, by home and id.
	keys = map[string]map[[idLen]byte][]byte{}
	// current is the key new blobs are sealed with, per home.
	current = map[string][idLen]byte{}
	// lockedUntil: an OS keystore that just said "locked" is not asked again
	// for a while, so a run of reads over SSH pays the timeout once.
	lockedUntil time.Time
	// fellBack is logged once per process.
	fellBack bool
)

const lockedBackoff = 30 * time.Second

// Indirection for tests.
var (
	osStoreFor   = osStore
	osUsable     = osStoreUsable
	allowOSStore = func() bool { return osUsable() && os.Getenv("REMINAL_KEYSTORE") != "file" }
)

// osStoreUsable reports whether this process may touch the OS keystore at
// all, to read or to write: never from a test binary, and never when HOME is
// not this user's real home — a test or a throwaway rig must not reach into
// the person's own keychain, not even to look. (REMINAL_KEYSTORE=file only
// keeps NEW keys out of it.)
func osStoreUsable() bool {
	if strings.HasSuffix(strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe"), ".test") {
		return false
	}
	u, err := user.Current()
	if err != nil {
		return false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	return canonical(u.HomeDir) == canonical(home)
}

// canonical resolves symlinks and, where the file system ignores case, folds
// it, so two processes that spell one home differently agree on it.
func canonical(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		a = filepath.Clean(p)
	}
	if e, err := filepath.EvalSymlinks(a); err == nil {
		a = e
	}
	if caseInsensitiveFS {
		a = strings.ToLower(a)
	}
	return a
}

func readMeta(dir string) (*meta, error) {
	b, err := os.ReadFile(metaPath(dir))
	if err != nil {
		return nil, err
	}
	var m meta
	if json.Unmarshal(b, &m) != nil || m.Source == "" || m.ID == "" {
		// Damaged, not absent: which key it named is unknown, so nothing may
		// be called gone and no key may be made in its place.
		return nil, errMetaCorrupt
	}
	return &m, nil
}

var errMetaCorrupt = errors.New("atrest.json is damaged")

func writeMeta(dir string, m meta) error {
	b, _ := json.Marshal(m)
	return atomicfile.Write(metaPath(dir), b, 0o600)
}

// osStoreAt is the OS keystore for this home, under the account atrest.json
// recorded (or the one this home would get).
func osStoreAt(dir string, m *meta) store {
	if !osUsable() {
		return nil
	}
	acct := ""
	if m != nil {
		acct = m.Account
	}
	if acct == "" {
		acct = keystoreAccount(canonical(dir))
	}
	return osStoreFor(dir, acct)
}

func remember(dir string, k []byte) [idLen]byte {
	id := keyID(k)
	if keys[dir] == nil {
		keys[dir] = map[[idLen]byte][]byte{}
	}
	keys[dir][id] = k
	return id
}

func osLocked() bool { return time.Now().Before(lockedUntil) }

// fromStore asks st for its key and remembers it. ok=false with locked=true
// when the store could not answer.
func fromStore(dir string, st store) (id [idLen]byte, ok, locked bool) {
	if st == nil {
		return id, false, false
	}
	isOS := st.source() != srcFile
	if isOS && osLocked() {
		return id, false, true
	}
	k, err := st.get()
	if err == nil && len(k) == keyLen {
		return remember(dir, k), true, false
	}
	if errors.Is(err, errNotFound) {
		return id, false, false
	}
	if isOS {
		lockedUntil = time.Now().Add(lockedBackoff)
	}
	return id, false, true
}

// storeAt is the store atrest.json names; nil when this build or context
// has no such store (the Secret Service from a shell with no session bus).
func storeAt(dir string, m *meta) store {
	if m.Source == "file" {
		return fileStore{dir: dir}
	}
	if st := osStoreAt(dir, m); st != nil && st.name() == m.Source {
		return st
	}
	return nil
}

// sealingKey returns the key to seal with: the one atrest.json names, made
// the first time. A store that cannot answer right now (a locked keychain, a
// keyring with no session bus) sends the blob to the file key instead — the
// same fallback a machine with no keystore gets — so nothing goes unsaved;
// such blobs open from the file later whatever the keystore does.
func sealingKey(dir string) ([]byte, [idLen]byte, byte, error) {
	var zero [idLen]byte
	if m, err := readMeta(dir); err == nil {
		if id, ok := current[dir]; ok && hex.EncodeToString(id[:]) == m.ID {
			return keys[dir][id], id, sourceByte(m.Source), nil
		}
		st := storeAt(dir, m)
		id, ok, locked := fromStore(dir, st)
		switch {
		case ok && hex.EncodeToString(id[:]) == m.ID:
			current[dir] = id
			if st.source() != srcFile {
				promoteFallback(dir, m)
			}
			return keys[dir][id], id, st.source(), nil
		case locked || st == nil:
			return fileFallback(dir)
		}
		// The store answered and the key is not there (or is another
		// key): it is gone, make a new one.
	} else if !errors.Is(err, os.ErrNotExist) {
		return fileFallback(dir)
	}
	if err := mint(dir); err != nil {
		if errors.Is(err, ErrLocked) {
			return fileFallback(dir)
		}
		return nil, zero, 0, err
	}
	m, err := readMeta(dir)
	if err != nil {
		return nil, zero, 0, ErrLocked
	}
	for id, k := range keys[dir] {
		if hex.EncodeToString(id[:]) == m.ID {
			current[dir] = id
			return k, id, sourceByte(m.Source), nil
		}
	}
	return nil, zero, 0, ErrLocked
}

// fileFallback seals with the 0600 key file, making it if needed, without
// touching atrest.json.
func fileFallback(dir string) ([]byte, [idLen]byte, byte, error) {
	var zero [idLen]byte
	fs := fileStore{dir: dir}
	if id, ok, _ := fromStore(dir, fs); ok {
		noteFallback(dir)
		return keys[dir][id], id, srcFile, nil
	}
	unlock, err := lockFile(dir, "atrest-file.lock", keystoreTimeout)
	if err != nil {
		return nil, zero, 0, err
	}
	defer unlock()
	if id, ok, _ := fromStore(dir, fs); ok {
		noteFallback(dir)
		return keys[dir][id], id, srcFile, nil
	}
	if _, err := os.Lstat(fs.path()); err == nil {
		return nil, zero, 0, ErrLocked // there but unreadable: never replaced
	}
	k := make([]byte, keyLen)
	if _, err := rand.Read(k); err != nil {
		return nil, zero, 0, err
	}
	if err := fs.put(k); err != nil {
		return nil, zero, 0, err
	}
	noteFallback(dir)
	return k, remember(dir, k), srcFile, nil
}

func noteFallback(dir string) {
	if !fellBack {
		fellBack = true
		Logf("reminal: the OS keystore is not answering; saving with the key file in %s for now", dir)
	}
}

// mint makes the key, under a lock so sessions starting together agree on
// one. With no atrest.json it first adopts a key a store already holds (the
// metadata was lost), the OS keystore's before the file's. With an
// atrest.json whose store says the key is gone it makes a new one.
func mint(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	unlock, err := lockDir(dir)
	if err != nil {
		return err
	}
	defer unlock()
	m, merr := readMeta(dir)
	if merr != nil && !errors.Is(merr, os.ErrNotExist) {
		return ErrLocked
	}
	if merr == nil {
		// Someone else may have made it while we waited.
		st := storeAt(dir, m)
		id, ok, locked := fromStore(dir, st)
		if ok && hex.EncodeToString(id[:]) == m.ID {
			return nil
		}
		if ok {
			// The store holds a key, just not the one atrest.json names
			// (an older ~/.reminal restored from a backup): keep what the
			// store has — never write over a key that exists.
			m.ID = hex.EncodeToString(id[:])
			return writeMeta(dir, *m)
		}
		if locked || st == nil {
			return ErrLocked
		}
	} else {
		m = nil
		ost := osStoreAt(dir, nil)
		if id, ok, locked := fromStore(dir, ost); ok {
			return writeMeta(dir, meta{V: 1, Source: ost.name(), ID: hex.EncodeToString(id[:]), Account: keystoreAccount(canonical(dir))})
		} else if locked {
			return ErrLocked // the file fallback takes over; atrest.json stays unwritten
		}
		if id, ok, _ := fromStore(dir, fileStore{dir: dir}); ok {
			// A key file made while the keystore was not answering (a first
			// start over SSH): move it into the keystore now that it is,
			// rather than stay on the file for good.
			if promoteFileKey(dir, keys[dir][id]) {
				return nil
			}
			return writeMeta(dir, meta{V: 1, Source: "file", ID: hex.EncodeToString(id[:])})
		}
	}
	acct := keystoreAccount(canonical(dir))
	if m != nil && m.Account != "" {
		acct = m.Account
	}
	k := make([]byte, keyLen)
	if _, err := rand.Read(k); err != nil {
		return err
	}
	if ost := osStoreAt(dir, &meta{Account: acct}); ost != nil && allowOSStore() {
		if err := ost.put(k); err == nil {
			// Read it back: what we wrote is what we will read later.
			if got, err := ost.get(); err == nil && bytes.Equal(got, k) {
				id := remember(dir, k)
				return writeMeta(dir, meta{V: 1, Source: ost.name(), ID: hex.EncodeToString(id[:]), Account: acct})
			}
		}
		Logf("reminal: the %s did not take the at-rest key; using a file in %s instead", ost.name(), dir)
	}
	fs := fileStore{dir: dir}
	old, ferr := fs.get()
	if errors.Is(ferr, ErrLocked) {
		return ErrLocked // a key file that cannot be read is never replaced
	}
	if ferr == nil && len(old) == keyLen {
		// The file holds a fallback key blobs may already be sealed with:
		// keep it, and make it the key.
		id := remember(dir, old)
		return writeMeta(dir, meta{V: 1, Source: "file", ID: hex.EncodeToString(id[:])})
	}
	if err := fs.put(k); err != nil {
		return err
	}
	id := remember(dir, k)
	return writeMeta(dir, meta{V: 1, Source: "file", ID: hex.EncodeToString(id[:])})
}

// promoteFileKey moves the key file's key into the OS keystore and deletes
// the file, once the keystore has it back verbatim.
func promoteFileKey(dir string, k []byte) bool {
	acct := keystoreAccount(canonical(dir))
	ost := osStoreAt(dir, &meta{Account: acct})
	if ost == nil || !allowOSStore() || ost.put(k) != nil {
		return false
	}
	if got, err := ost.get(); err != nil || !bytes.Equal(got, k) {
		return false
	}
	id := keyID(k)
	if writeMeta(dir, meta{V: 1, Source: ost.name(), ID: hex.EncodeToString(id[:]), Account: acct,
		Promoted: []string{hex.EncodeToString(id[:])}}) != nil {
		return false
	}
	_ = os.Remove(fileStore{dir: dir}.path())
	SweepTemps(dir, 0)
	return true
}

// openingKey finds the key with this id, starting with the store the blob's
// header names: one already seen, then that store (and, for an OS keystore,
// a fallback key kept there by id), then the rest. ErrKeyGone only when every
// store answered without it; a store that could not be asked — locked, slow,
// absent in this context (no session bus) — or a damaged atrest.json makes it
// ErrLocked.
func openingKey(dir string, want [idLen]byte, src byte) ([]byte, error) {
	if k, ok := keys[dir][want]; ok {
		return k, nil
	}
	m, merr := readMeta(dir)
	locked := merr != nil && !errors.Is(merr, os.ErrNotExist)
	tried := map[string]bool{}
	try := func(st store, label string) []byte {
		if tried[label] {
			return nil
		}
		tried[label] = true
		if st == nil {
			return nil
		}
		if _, _, l := fromStore(dir, st); l {
			locked = true
		}
		return keys[dir][want]
	}
	osAt := func(suffix string) store {
		acct := keystoreAccount(canonical(dir))
		if m != nil && m.Account != "" {
			acct = m.Account
		}
		if suffix != "" {
			acct += "." + suffix
		}
		return osStoreAt(dir, &meta{Account: acct})
	}
	// Only a home that has only ever used the key file may conclude a key
	// is gone without asking an OS keystore: anywhere else the key may have
	// been moved into one (see promoteFallback), so a keystore that cannot
	// be reached from here means "later".
	fileOnlyHome := m != nil && m.Source == "file" && len(m.Promoted) == 0
	tryOS := func() []byte {
		st := osAt("")
		if st == nil && (src != srcFile || !fileOnlyHome || promoted(m, want)) {
			locked = true // the store that may hold it cannot be reached from here
		}
		if k := try(st, "os"); k != nil {
			return k
		}
		return try(osAt(hex.EncodeToString(want[:])), "os-by-id")
	}
	order := []func() []byte{tryOS, func() []byte { return try(fileStore{dir: dir}, "file") }}
	if src == srcFile {
		order[0], order[1] = order[1], order[0]
	}
	for _, f := range order {
		if k := f(); k != nil {
			return k, nil
		}
	}
	if locked {
		return nil, ErrLocked
	}
	return nil, ErrKeyGone
}

// promoteFallback moves a key file made while the keystore was not answering
// into the keystore, kept under its id so blobs sealed with it still open,
// and deletes the file — once the keystore answers again.
func promoteFallback(dir string, m *meta) {
	fs := fileStore{dir: dir}
	if _, err := os.Lstat(fs.path()); err != nil {
		return
	}
	unlock, err := lockFile(dir, "atrest-file.lock", 0)
	if err != nil {
		return // someone else is on it; try at the next save
	}
	defer unlock()
	k, err := fs.get()
	if err != nil {
		return
	}
	id := keyID(k)
	acct := m.Account
	if acct == "" {
		acct = keystoreAccount(canonical(dir))
	}
	st := osStoreAt(dir, &meta{Account: acct + "." + hex.EncodeToString(id[:])})
	if st == nil || !allowOSStore() {
		return
	}
	if got, err := st.get(); err != nil || !bytes.Equal(got, k) {
		if st.put(k) != nil {
			return
		}
		if got, err := st.get(); err != nil || !bytes.Equal(got, k) {
			return
		}
	}
	remember(dir, k)
	notePromoted(dir, id)
	_ = os.Remove(fs.path())
	SweepTemps(dir, 0)
}

func promoted(m *meta, id [idLen]byte) bool {
	if m == nil {
		return false
	}
	h := hex.EncodeToString(id[:])
	for _, p := range m.Promoted {
		if p == h {
			return true
		}
	}
	return false
}

// notePromoted records in atrest.json that id now lives in the keystore.
func notePromoted(dir string, id [idLen]byte) {
	m, err := readMeta(dir)
	if err != nil || promoted(m, id) {
		return
	}
	m.Promoted = append(m.Promoted, hex.EncodeToString(id[:]))
	_ = writeMeta(dir, *m)
}

func sourceByte(name string) byte {
	switch name {
	case "keychain":
		return srcKeychain
	case "dpapi":
		return srcDPAPI
	case "secret-service":
		return srcSecretSvc
	}
	return srcFile
}

// lockDir takes ~/.reminal/atrest.lock, held while the key is made. A
// process that cannot get it soon gives up with ErrLocked and saves with the
// key file meanwhile (which has its own lock), so a slow keystore holding it
// up does not hold up a session start for long.
func lockDir(dir string) (func(), error) {
	return lockFile(dir, "atrest.lock", 2*keystoreTimeout)
}

// lockFile takes an OS file lock, which the kernel lets go of if the holder
// dies: no stale lock, no two holders.
func lockFile(dir, name string, wait time.Duration) (func(), error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	for {
		if tryLock(f) {
			return func() { unlockFile(f); _ = f.Close() }, nil
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, ErrLocked
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// Backend names where the key lives now ("keychain", "dpapi",
// "secret-service", "file"), or "" when there is none yet. For doctor/info.
func Backend() string {
	dir, err := Dir()
	if err != nil {
		return ""
	}
	m, err := readMeta(dir)
	if err != nil {
		return ""
	}
	return m.Source
}

// Status says whether the key atrest.json names can be had right now: "ok"
// (or no key yet), "locked" (the store did not answer) or "gone" (it answered
// without it; a new one is made at the next save). For doctor.
func Status() string {
	dir, err := Dir()
	if err != nil {
		return "locked"
	}
	keyMu.Lock()
	defer keyMu.Unlock()
	canaryOn = false // a check writes nothing
	// Without the canary a "not found" reads as locked; that must not put
	// the store on the back-off list for the real reads that follow.
	wasLocked := lockedUntil
	defer func() { canaryOn = true; lockedUntil = wasLocked }()
	m, err := readMeta(dir)
	if errors.Is(err, os.ErrNotExist) {
		return "ok"
	}
	if err != nil {
		return "locked"
	}
	st := storeAt(dir, m)
	id, ok, locked := fromStore(dir, st)
	switch {
	case ok && hex.EncodeToString(id[:]) == m.ID:
		return "ok"
	case locked || st == nil:
		return "locked"
	}
	return "gone"
}

// ---- sealing -------------------------------------------------------------------

// Seal seals plaintext under the at-rest key, bound to kind and id: a blob
// sealed for one session's record does not open as another's. It makes the
// key the first time.
func Seal(kind, id string, plaintext []byte) ([]byte, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	keyMu.Lock()
	if unavailableForTest {
		keyMu.Unlock()
		return nil, ErrLocked
	}
	k, kid, src, err := sealingKey(dir)
	keyMu.Unlock()
	if err != nil {
		return nil, err
	}
	return seal(k, kid, src, kind, id, plaintext)
}

// Open opens a blob made by Seal. ErrNotSealed for plain bytes (an older
// version's file), ErrLocked to try later, ErrKeyGone or ErrCorrupt when it
// will never open — quarantine it.
func Open(kind, id string, blob []byte) ([]byte, error) {
	if !IsSealed(blob) {
		return nil, ErrNotSealed
	}
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	var bid [idLen]byte
	copy(bid[:], blob[len(magic)+2:len(magic)+2+idLen])
	keyMu.Lock()
	if unavailableForTest {
		keyMu.Unlock()
		return nil, ErrLocked
	}
	k, err := openingKey(dir, bid, blob[len(magic)+1])
	keyMu.Unlock()
	if err != nil {
		return nil, err
	}
	return open(k, kind, id, blob)
}

// NewKey returns a fresh one-time key, for a hand-off between two processes
// that never touches a keystore (the hot-restart scrollback dump).
func NewKey() ([]byte, error) {
	k := make([]byte, keyLen)
	_, err := rand.Read(k)
	return k, err
}

// SealWith seals under a caller-held key (see NewKey).
func SealWith(key []byte, kind, id string, plaintext []byte) ([]byte, error) {
	if len(key) != keyLen {
		return nil, errors.New("at-rest: bad key length")
	}
	return seal(key, keyID(key), srcEphemeral, kind, id, plaintext)
}

// OpenWith opens a blob made by SealWith.
func OpenWith(key []byte, kind, id string, blob []byte) ([]byte, error) {
	if !IsSealed(blob) {
		return nil, ErrNotSealed
	}
	if len(key) != keyLen {
		return nil, ErrKeyGone
	}
	return open(key, kind, id, blob)
}

// IsSealed reports whether b starts like a sealed blob.
func IsSealed(b []byte) bool {
	return len(b) >= headerLen+16 && string(b[:len(magic)]) == magic && b[len(magic)] == version
}

func aad(header []byte, kind, id string) []byte {
	a := append([]byte{}, header...)
	a = append(a, kind...)
	a = append(a, 0)
	return append(a, id...)
}

func seal(key []byte, kid [idLen]byte, src byte, kind, id string, pt []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	h := make([]byte, 0, headerLen+len(pt)+gcm.Overhead())
	h = append(h, magic...)
	h = append(h, version, src)
	h = append(h, kid[:]...)
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	h = append(h, nonce...)
	return gcm.Seal(h, nonce, pt, aad(h[:headerLen], kind, id)), nil
}

func open(key []byte, kind, id string, blob []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	header := blob[:headerLen]
	nonce := header[headerLen-gcm.NonceSize():]
	pt, err := gcm.Open(nil, nonce, blob[headerLen:], aad(header, kind, id))
	if err != nil {
		return nil, ErrCorrupt
	}
	return pt, nil
}

// ---- quarantine ----------------------------------------------------------------

// QuarantineKeep is how long quarantined files are kept before PruneQuarantine
// removes them.
const QuarantineKeep = 7 * 24 * time.Hour

// Quarantine moves files that will not open into dir/quarantine, with a note
// saying why, instead of deleting them: a keystore that only looked gone (a
// profile repair, a slow login) must not cost anyone their sessions for good.
func Quarantine(dir, name, reason string, files ...string) error {
	q := filepath.Join(dir, "quarantine")
	if err := os.MkdirAll(q, 0o700); err != nil {
		return err
	}
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	moved := 0
	for _, f := range files {
		if _, err := os.Stat(f); err != nil {
			continue
		}
		dst := filepath.Join(q, stamp+"-"+filepath.Base(f))
		if err := os.Rename(f, dst); err == nil {
			// The keep counts from now, not from the file's last save.
			now := time.Now()
			_ = os.Chtimes(dst, now, now)
			moved++
		}
	}
	if moved == 0 {
		return nil
	}
	note := fmt.Sprintf("%s\nquarantined %s; removed after %s\n", reason,
		time.Now().Format(time.RFC3339), time.Now().Add(QuarantineKeep).Format(time.RFC3339))
	return os.WriteFile(filepath.Join(q, stamp+"-"+name+".reason"), []byte(note), 0o600)
}

// PruneQuarantine removes quarantined files older than QuarantineKeep and
// returns how many sessions are still held there.
func PruneQuarantine(dir string) int {
	q := filepath.Join(dir, "quarantine")
	ents, err := os.ReadDir(q)
	if err != nil {
		return 0
	}
	held := 0
	for _, e := range ents {
		fi, err := e.Info()
		if err != nil {
			continue
		}
		if time.Since(fi.ModTime()) > QuarantineKeep {
			_ = os.Remove(filepath.Join(q, e.Name()))
			continue
		}
		if filepath.Ext(e.Name()) == ".reason" {
			held++
		}
	}
	return held
}

// ResetCacheForTest forgets the in-process key, as a new process would.
func ResetCacheForTest() {
	keyMu.Lock()
	keys, current, lockedUntil, fellBack = map[string]map[[idLen]byte][]byte{}, map[string][idLen]byte{}, time.Time{}, false
	keyMu.Unlock()
}

// Lock takes an OS file lock on dir/name, waiting up to wait. The kernel lets
// it go if the holder dies. For callers that must not interleave (two
// processes migrating the same record).
func Lock(dir, name string, wait time.Duration) (func(), error) {
	return lockFile(dir, name, wait)
}

// SweepTemps removes atomic-write temp files in dir older than age (a crash
// mid-write leaves them; one from a key write holds the key). Zero age still
// spares files a live writer may be using right now.
func SweepTemps(dir string, age time.Duration) {
	if age < time.Minute {
		age = time.Minute
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		if !strings.HasPrefix(e.Name(), ".reminal-") || !strings.HasSuffix(e.Name(), ".tmp") {
			continue
		}
		if fi, err := e.Info(); err == nil && time.Since(fi.ModTime()) > age {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// UnavailableForTest makes every Seal and Open answer ErrLocked, as a
// keystore that will not answer does. For tests of callers.
func UnavailableForTest(on bool) {
	keyMu.Lock()
	unavailableForTest = on
	keyMu.Unlock()
}

var unavailableForTest bool
