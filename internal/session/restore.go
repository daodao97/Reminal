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

// RestoreScrollbackPath is where a session's scrollback is kept for restore.
func RestoreScrollbackPath(id string) (string, error) { return restorePath(id, ".scrollback.json") }

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

// WriteRestore saves (replaces) a session's restore record.
func WriteRestore(r Restore) error {
	p, err := restorePath(r.ID, ".json")
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(p, data)
}

// ReadRestore reads one session's restore record.
func ReadRestore(id string) (*Restore, error) {
	p, err := restorePath(strings.ToUpper(id), ".json")
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var r Restore
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// ReadRestores lists every restore record, oldest save first — running
// sessions included; the caller decides what is not running.
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
	var out []Restore
	for _, e := range ents {
		n := e.Name()
		if !strings.HasSuffix(n, ".json") || strings.Contains(n, ".scrollback") {
			continue
		}
		if r, err := ReadRestore(strings.TrimSuffix(n, ".json")); err == nil && r.ID != "" {
			out = append(out, *r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SavedAt.Before(out[j].SavedAt) })
	return out, nil
}

// ClearRestore forgets a session for good: its record, its scrollback and
// its agent's conversation id.
func ClearRestore(id string) error {
	for _, suf := range []string{".json", ".scrollback.json", ".conv"} {
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
