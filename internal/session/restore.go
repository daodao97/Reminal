// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"reminal/internal/atrest"
)

// Restore is what brings a session back after its machine restarted: who it
// was (id, PIN, the credentials the relay knows it by), where its shell was,
// and which coding agent was running in it — with the id of that agent's
// conversation when the agent's hook told us. Its processes are gone; this
// is enough to start new ones that pick up where those left off.
//
// Unlike the active record it is not pruned when the process dies — that is
// exactly when it is needed. It goes only when the session is ended on
// purpose: `reminal kill`, `reminal stop`, or the shell exiting by itself.
type Restore struct {
	ID       string    `json:"id"`
	PIN      string    `json:"pin"`
	PinHash  string    `json:"pin_hash,omitempty"`
	Token    string    `json:"token,omitempty"`
	Name     string    `json:"name,omitempty"`
	Cwd      string    `json:"cwd,omitempty"`
	Headless bool      `json:"headless,omitempty"`
	Fg       string    `json:"fg,omitempty"`      // the coding agent in the foreground, by name
	FgArgs   []string  `json:"fg_args,omitempty"` // its command line
	Conv     string    `json:"conv,omitempty"`    // its conversation id, from its hook
	SavedAt  time.Time `json:"saved_at"`
}

func restoreDir() (string, error) {
	dir, err := activeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "restore"), nil
}

func restorePath(id, suffix string) (string, error) {
	if id == "" || strings.ContainsAny(id, `/\.`) {
		return "", errors.New("restore record requires a session id")
	}
	dir, err := restoreDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, id+suffix), nil
}

// RestoreScrollbackPath is where a session's scrollback is kept for restore,
// sealed (see SealScrollback).
func RestoreScrollbackPath(id string) (string, error) { return restorePath(id, ".scrollback.sealed") }

// LegacyScrollbackPath is where versions before sealing kept it, in the clear.
func LegacyScrollbackPath(id string) (string, error) { return restorePath(id, ".scrollback.json") }

// Kinds a sealed restore file is bound to: a record never opens as a
// scrollback, nor one session's as another's.
const (
	kindRestore    = "restore"
	kindScrollback = "scrollback"
)

// SealScrollback / OpenScrollback seal a session's restore scrollback.
func SealScrollback(id string, b []byte) ([]byte, error) {
	return atrest.Seal(kindScrollback, strings.ToUpper(id), b)
}
func OpenScrollback(id string, b []byte) ([]byte, error) {
	return atrest.Open(kindScrollback, strings.ToUpper(id), b)
}

func writeFileAtomic(p string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	_ = os.Remove(p) // Windows rename will not replace
	return os.Rename(tmp, p)
}

// WriteRestore saves (replaces) a session's restore record, sealed. When the
// keystore will not give the key up right now it writes nothing (the next
// save tick tries again) — never the record in the clear.
func WriteRestore(r Restore) error {
	p, err := restorePath(r.ID, ".sealed")
	if err != nil {
		return err
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	blob, err := atrest.Seal(kindRestore, r.ID, data)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(p, blob); err != nil {
		return err
	}
	// The plain record an older version left is superseded.
	if lp, err := restorePath(r.ID, ".json"); err == nil {
		_ = os.Remove(lp)
	}
	return nil
}

// ReadRestore reads one session's restore record: the sealed one, else a
// plain one an older version wrote (sealed on the way, when it can be). A
// record that will never open (its key is gone, it is damaged) is moved to
// quarantine and reported as missing. atrest.ErrLocked means try later.
func ReadRestore(id string) (*Restore, error) {
	id = strings.ToUpper(id)
	p, err := restorePath(id, ".sealed")
	if err != nil {
		return nil, err
	}
	blob, err := os.ReadFile(p)
	if err == nil {
		data, oerr := atrest.Open(kindRestore, id, blob)
		if oerr != nil {
			if errors.Is(oerr, atrest.ErrLocked) {
				return nil, oerr
			}
			quarantineRestore(id, oerr)
			return nil, os.ErrNotExist
		}
		var r Restore
		if err := json.Unmarshal(data, &r); err != nil || r.ID != id {
			quarantineRestore(id, atrest.ErrCorrupt)
			return nil, os.ErrNotExist
		}
		return &r, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return readLegacyRestore(id)
}

// readLegacyRestore reads a plain record from before sealing and, when the
// key can be had, replaces it (and its plain scrollback) with sealed copies.
// The plain copies go only once the sealed ones are safely written.
func readLegacyRestore(id string) (*Restore, error) {
	lp, err := restorePath(id, ".json")
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(lp)
	if err != nil {
		return nil, err
	}
	var r Restore
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	if r.ID == "" {
		r.ID = id
	}
	_ = migrateLegacy(r)
	return &r, nil
}

func migrateLegacy(r Restore) error {
	if lsp, err := LegacyScrollbackPath(r.ID); err == nil {
		if pt, err := os.ReadFile(lsp); err == nil {
			blob, err := SealScrollback(r.ID, pt)
			if err != nil {
				return err
			}
			sp, _ := RestoreScrollbackPath(r.ID)
			if err := writeFileAtomic(sp, blob); err != nil {
				return err
			}
			_ = os.Remove(lsp)
			_ = os.Remove(lsp + ".tmp")
		}
	}
	return WriteRestore(r) // removes the plain record once the sealed one is in
}

// quarantineRestore moves a session's sealed files out of the way with a note.
func quarantineRestore(id string, why error) {
	dir, err := restoreDir()
	if err != nil {
		return
	}
	var files []string
	for _, suf := range []string{".sealed", ".scrollback.sealed", ".conv"} {
		if p, err := restorePath(id, suf); err == nil {
			files = append(files, p)
		}
	}
	reason := "session " + id + ": " + why.Error()
	if atrest.Quarantine(dir, id, reason, files...) == nil {
		atrest.Logf("reminal: could not open the saved session %s (%v); moved it to %s for %d days",
			id, why, filepath.Join(dir, "quarantine"), int(atrest.QuarantineKeep.Hours()/24))
	}
}

// QuarantinedRestores is how many sessions sit in quarantine (pruning the
// ones past their keep), for doctor/info.
func QuarantinedRestores() int {
	dir, err := restoreDir()
	if err != nil {
		return 0
	}
	return atrest.PruneQuarantine(dir)
}

// ReadRestores lists every restore record, oldest save first — running
// sessions included; the caller decides what is not running. Plain records
// from an older version are sealed on the way. atrest.ErrLocked (with the
// records it could read) when some could not be opened yet.
func ReadRestores() ([]Restore, error) {
	dir, err := restoreDir()
	if err != nil {
		return nil, err
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	seen := map[string]bool{}
	var out []Restore
	var locked error
	for _, e := range ents {
		n := e.Name()
		var id string
		switch {
		case strings.HasSuffix(n, ".scrollback.sealed"), strings.HasSuffix(n, ".scrollback.json"):
			continue
		case strings.HasSuffix(n, ".sealed"):
			id = strings.TrimSuffix(n, ".sealed")
		case strings.HasSuffix(n, ".json"):
			id = strings.TrimSuffix(n, ".json")
		default:
			continue
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		r, err := ReadRestore(id)
		if errors.Is(err, atrest.ErrLocked) {
			locked = err
			continue
		}
		if err == nil && r.ID != "" {
			out = append(out, *r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SavedAt.Before(out[j].SavedAt) })
	return out, locked
}

// ClearRestore forgets a session for good: its record, its scrollback and
// its agent's conversation id, sealed or from an older version.
func ClearRestore(id string) error {
	for _, suf := range []string{".sealed", ".json", ".scrollback.sealed", ".scrollback.json", ".conv"} {
		if p, err := restorePath(strings.ToUpper(id), suf); err == nil {
			_ = os.Remove(p)
			_ = os.Remove(p + ".tmp")
		}
	}
	return nil
}

// WriteConv records the conversation id a coding agent's hook reported for
// the session it runs in — what lets it be resumed by id, not "the latest".
func WriteConv(id, conv string) error {
	p, err := restorePath(id, ".conv")
	if err != nil {
		return err
	}
	return writeFileAtomic(p, []byte(conv))
}

// ReadConv is the last conversation id reported for a session, or "".
func ReadConv(id string) string {
	p, err := restorePath(id, ".conv")
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
