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

type meta struct {
	V      int    `json:"v"`
	Source string `json:"source"`
	ID     string `json:"id"`
}

func metaPath(dir string) string { return filepath.Join(dir, "atrest.json") }

func keyID(k []byte) [idLen]byte {
	s := sha256.Sum256(append([]byte("reminal at-rest key id\x00"), k...))
	var id [idLen]byte
	copy(id[:], s[:idLen])
	return id
}

// ---- the key -------------------------------------------------------------------

type cachedKey struct {
	dir     string
	key     []byte
	id      [idLen]byte
	src     byte
	metaMod time.Time
}

var (
	keyMu  sync.Mutex
	cached *cachedKey
	// lockedUntil: a keystore that just said "locked" is not asked again for
	// a while, so a run of reads over SSH pays the timeout once, not per file.
	lockedUntil time.Time
)

const lockedBackoff = 30 * time.Second

// storeFor names the store behind a source, whatever the policy for new keys:
// a key minted in the Keychain is read from the Keychain even under
// REMINAL_KEYSTORE=file.
func storeFor(dir string, src string) store {
	switch src {
	case "file":
		return fileStore{dir: dir}
	default:
		if s := osStoreFor(dir); s != nil && s.name() == src {
			return s
		}
		return nil
	}
}

// Indirection for tests.
var (
	osStoreFor   = osStore
	allowOSStore = osStoreAllowed
)

// osStoreAllowed reports whether a NEW key may go into the OS keystore: not
// when asked not to, and not when HOME is not this user's real home — a test
// or a throwaway rig must never write into the person's own keychain.
func osStoreAllowed() bool {
	if os.Getenv("REMINAL_KEYSTORE") == "file" || strings.HasSuffix(strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe"), ".test") {
		return false // never from a test binary, whatever HOME says
	}
	u, err := user.Current()
	if err != nil {
		return false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	a, _ := filepath.Abs(u.HomeDir)
	b, _ := filepath.Abs(home)
	if ea, err := filepath.EvalSymlinks(a); err == nil {
		a = ea
	}
	if eb, err := filepath.EvalSymlinks(b); err == nil {
		b = eb
	}
	return sameDir(a, b)
}

// currentKey returns the key, minting one when none exists anywhere.
// mint=false never creates one (Open must not).
func currentKey(mint bool) (*cachedKey, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	keyMu.Lock()
	defer keyMu.Unlock()
	fi, statErr := os.Stat(metaPath(dir))
	if cached != nil && cached.dir == dir && statErr == nil && fi.ModTime().Equal(cached.metaMod) {
		return cached, nil
	}
	cached = nil
	noMeta := errors.Is(statErr, os.ErrNotExist)
	if time.Now().Before(lockedUntil) && !(mint && noMeta) {
		return nil, ErrLocked
	}
	k, err := loadKey(dir)
	if err == nil {
		cached = k
		return k, nil
	}
	if errors.Is(err, ErrLocked) {
		lockedUntil = time.Now().Add(lockedBackoff)
	}
	// Locked with no key ever recorded here: the OS keystore is not
	// answering on a first start, so the key goes in a file — a session
	// must not go without its saved details because of it.
	if !mint || (errors.Is(err, ErrLocked) && !noMeta) {
		return nil, err
	}
	// No key at all, or the keystore says it is gone: make one.
	k, err = mintKey(dir, true)
	if err != nil {
		return nil, err
	}
	cached = k
	return k, nil
}

// loadKey reads the key named by the metadata. With no metadata it looks for
// a key a lost metadata file used to name — in the OS keystore, then the key
// file — so a deleted atrest.json does not orphan everything sealed before.
// ErrKeyGone only when every store that could hold it answered "none".
func loadKey(dir string) (*cachedKey, error) {
	b, err := os.ReadFile(metaPath(dir))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, ErrLocked
	}
	if err != nil {
		return discoverKey(dir)
	}
	var m meta
	if json.Unmarshal(b, &m) != nil || m.Source == "" {
		return discoverKey(dir)
	}
	st := storeFor(dir, m.Source)
	if st == nil {
		// Names a keystore this build or context cannot reach (the Secret
		// Service from a shell with no D-Bus): locked, not gone.
		return nil, ErrLocked
	}
	k, err := st.get()
	if errors.Is(err, errNotFound) {
		return nil, ErrKeyGone
	}
	if err != nil || len(k) != keyLen {
		return nil, ErrLocked
	}
	id := keyID(k)
	if hex.EncodeToString(id[:]) != m.ID {
		return nil, ErrKeyGone
	}
	return newCached(dir, k, st), nil
}

func discoverKey(dir string) (*cachedKey, error) {
	locked := false
	if ost := osStoreFor(dir); ost != nil && !time.Now().Before(lockedUntil) {
		k, err := ost.get()
		if err == nil && len(k) == keyLen {
			return newCached(dir, k, ost), nil
		}
		locked = !errors.Is(err, errNotFound)
	} else if ost != nil {
		locked = true // it just said "locked"; don't wait on it again
	}
	fs := fileStore{dir: dir}
	if k, err := fs.get(); err == nil {
		return newCached(dir, k, fs), nil
	} else if !errors.Is(err, errNotFound) {
		locked = true
	}
	if locked {
		return nil, ErrLocked
	}
	return nil, ErrKeyGone
}

func newCached(dir string, k []byte, st store) *cachedKey {
	ck := &cachedKey{dir: dir, key: k, id: keyID(k), src: st.source()}
	if fi, err := os.Stat(metaPath(dir)); err == nil {
		ck.metaMod = fi.ModTime()
	}
	return ck
}

// writeMeta records where the key lives.
func writeMeta(dir string, k []byte, st store) error {
	id := keyID(k)
	m, _ := json.Marshal(meta{V: 1, Source: st.name(), ID: hex.EncodeToString(id[:])})
	return atomicfile.Write(metaPath(dir), m, 0o600)
}

// mintKey makes the key, under a lock so sessions starting together agree on
// one. A key the stores already hold (lost metadata) is adopted, not
// replaced. replace=true makes a new one even so: the metadata named a key
// its store has since said is gone.
func mintKey(dir string, replace bool) (*cachedKey, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	unlock, err := lockDir(dir)
	if err != nil {
		return nil, err
	}
	defer unlock()
	// Someone else may have made it while we waited.
	fileOnly := false
	k, err := loadKey(dir)
	switch {
	case err == nil:
		if _, serr := os.Stat(metaPath(dir)); serr != nil {
			if st := storeFor(dir, sourceName(k.src)); st != nil {
				if err := writeMeta(dir, k.key, st); err != nil {
					return nil, err
				}
				k = newCached(dir, k.key, st)
			}
		}
		return k, nil
	case errors.Is(err, ErrLocked):
		if _, serr := os.Stat(metaPath(dir)); !errors.Is(serr, os.ErrNotExist) {
			return nil, err
		}
		fileOnly = true
	case !replace:
		if _, serr := os.Stat(metaPath(dir)); serr == nil {
			// The metadata names a key that is gone; only a Seal may replace it.
			return nil, ErrKeyGone
		}
	}
	key := make([]byte, keyLen)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	var st store
	if ost := osStoreFor(dir); ost != nil && allowOSStore() && !fileOnly {
		if err := ost.put(key); err == nil {
			// Read it back: what we wrote is what we will read later.
			if got, err := ost.get(); err == nil && bytes.Equal(got, key) {
				st = ost
			}
		}
		if st == nil {
			Logf("reminal: the %s is not available; the at-rest key goes in a file in %s instead", ost.name(), dir)
		}
	} else if fileOnly {
		Logf("reminal: the OS keystore is not answering; the at-rest key goes in a file in %s instead", dir)
	}
	if st == nil {
		fs := fileStore{dir: dir}
		if err := fs.put(key); err != nil {
			return nil, err
		}
		st = fs
	}
	if err := writeMeta(dir, key, st); err != nil {
		return nil, err
	}
	return newCached(dir, key, st), nil
}

func sourceName(src byte) string {
	switch src {
	case srcKeychain:
		return "keychain"
	case srcDPAPI:
		return "dpapi"
	case srcSecretSvc:
		return "secret-service"
	}
	return "file"
}

// lockDir takes ~/.reminal/atrest.lock. A lock older than lockStale was left
// by a process that died holding it.
const lockStale = 15 * time.Second

func lockDir(dir string) (func(), error) {
	p := filepath.Join(dir, "atrest.lock")
	deadline := time.Now().Add(lockStale + keystoreTimeout*3)
	for {
		f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = f.Close()
			return func() { _ = os.Remove(p) }, nil
		}
		if fi, serr := os.Stat(p); serr == nil && time.Since(fi.ModTime()) > lockStale {
			_ = os.Remove(p)
			continue
		}
		if time.Now().After(deadline) {
			return nil, ErrLocked
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Backend names where the key lives now ("keychain", "dpapi",
// "secret-service", "file"), or "" when there is none yet. For doctor/info.
func Backend() string {
	dir, err := Dir()
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(metaPath(dir))
	if err != nil {
		return ""
	}
	var m meta
	_ = json.Unmarshal(b, &m)
	return m.Source
}

// ---- sealing -------------------------------------------------------------------

// Seal seals plaintext under the at-rest key, bound to kind and id: a blob
// sealed for one session's record does not open as another's. It makes the
// key the first time. ErrLocked when the keystore holds a key it will not
// give up right now — the caller keeps what it has and tries again later.
func Seal(kind, id string, plaintext []byte) ([]byte, error) {
	k, err := currentKey(true)
	if err != nil {
		return nil, err
	}
	return seal(k.key, k.id, k.src, kind, id, plaintext)
}

// Open opens a blob made by Seal. ErrNotSealed for plain bytes (an older
// version's file), ErrLocked to try later, ErrKeyGone or ErrCorrupt when it
// will never open — quarantine it.
func Open(kind, id string, blob []byte) ([]byte, error) {
	if !IsSealed(blob) {
		return nil, ErrNotSealed
	}
	k, err := currentKey(false)
	if err != nil {
		return nil, err
	}
	var bid [idLen]byte
	copy(bid[:], blob[len(magic)+2:len(magic)+2+idLen])
	if bid != k.id {
		return nil, ErrKeyGone
	}
	return open(k.key, kind, id, blob)
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
	stamp := time.Now().UTC().Format("20060102T150405Z")
	moved := 0
	for _, f := range files {
		if _, err := os.Stat(f); err != nil {
			continue
		}
		if err := os.Rename(f, filepath.Join(q, stamp+"-"+filepath.Base(f))); err == nil {
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

func sameDir(a, b string) bool {
	if caseInsensitiveFS {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// ResetCacheForTest forgets the in-process key, as a new process would.
func ResetCacheForTest() {
	keyMu.Lock()
	cached, lockedUntil = nil, time.Time{}
	keyMu.Unlock()
}
