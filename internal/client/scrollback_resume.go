// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"reminal/internal/atrest"
)

// envResumeScrollback is the path of a 0600 dump written just before a hot
// restart. The session key is NOT carried across the exec (viewers re-EKE),
// so the dump is sealed under a one-time key handed to the successor in
// envResumeScrollbackKey — the key never touches disk — and the successor
// re-encrypts the history under its own session key.
const (
	envResumeScrollback    = "REMINAL_RESUME_SCROLLBACK"
	envResumeScrollbackKey = "REMINAL_RESUME_SCROLLBACK_KEY"
)

const kindHandoff = "handoff"

const scrollbackDumpVersion = 1

type scrollbackDump struct {
	Version  int               `json:"v"`
	NextSeq  uint64            `json:"next_seq"`
	BaseCols int               `json:"base_cols"`
	BaseRows int               `json:"base_rows"`
	Entries  []scrollDumpEntry `json:"entries"`
}

type scrollDumpEntry struct {
	Seq  uint64 `json:"seq"`
	Data []byte `json:"data,omitempty"`
	Bar  bool   `json:"bar,omitempty"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
}

func scrollbackDumpPath(sessionID string) (string, error) {
	dir, err := reminalDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "scrollback-"+sessionID+".json"), nil
}

// writeScrollbackDump writes the live buffer, sealed under a fresh one-time
// key, to a 0600 file the successor will load, and returns the key as hex.
// Empty history yields ("", "", nil) — no file.
func (a *Agent) writeScrollbackDump() (string, string, error) {
	if a == nil || a.buf == nil || a.box == nil || a.sessionID == "" {
		return "", "", nil
	}
	path, err := scrollbackDumpPath(a.sessionID)
	if err != nil {
		return "", "", err
	}
	if len(a.buf.From(0)) == 0 {
		return "", "", nil
	}
	key, err := atrest.NewKey()
	if err != nil {
		return "", "", err
	}
	id := a.sessionID
	err = a.writeScrollbackDumpTo(path, func(b []byte) ([]byte, error) {
		return atrest.SealWith(key, kindHandoff, id, b)
	})
	return path, hex.EncodeToString(key), err
}

// writeScrollbackDumpTo writes the live buffer, decrypted from the session
// key and sealed by seal, to path (0600). An empty buffer writes nothing.
func (a *Agent) writeScrollbackDumpTo(path string, seal func([]byte) ([]byte, error)) error {
	if a == nil || a.buf == nil || a.box == nil {
		return nil
	}
	raw := a.buf.From(0)
	if len(raw) == 0 {
		return nil
	}
	dump := scrollbackDump{
		Version: scrollbackDumpVersion,
		NextSeq: a.buf.LatestSeq(),
	}
	dump.BaseCols, dump.BaseRows = a.buf.Base()
	dump.Entries = make([]scrollDumpEntry, 0, len(raw))
	for _, e := range raw {
		de := scrollDumpEntry{Seq: e.Seq, Bar: e.Bar, Cols: e.Cols, Rows: e.Rows}
		if e.Data != "" {
			pt, err := a.box.Decrypt(e.Data)
			if err != nil {
				return fmt.Errorf("decrypt scrollback seq %d: %w", e.Seq, err)
			}
			de.Data = pt
		}
		dump.Entries = append(dump.Entries, de)
	}
	body, err := json.Marshal(dump)
	if err != nil {
		return err
	}
	body, err = seal(body)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	// Windows rename refuses to replace an existing dest. A leftover from a
	// failed restart would then make every later dump fail open (no history).
	// Crash between this remove and rename leaves only .tmp; take/remove
	// delete that too.
	_ = os.Remove(path)
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// dumpScrollbackForRestart writes the dump if there is history and returns
// the env entries that hand it to the successor. A write error must not
// block restart — we notify and continue without a file.
func (a *Agent) dumpScrollbackForRestart() (path string, env []string) {
	path, key, err := a.writeScrollbackDump()
	if err != nil {
		agentNotify("  reminal: could not save scrollback for restart — history will reset: %v\n", err)
		removeScrollbackDump(path)
		return "", nil
	}
	if path == "" {
		return "", nil
	}
	return path, []string{envResumeScrollback + "=" + path, envResumeScrollbackKey + "=" + key}
}

// removeScrollbackDump deletes a dump and its .tmp sibling. Safe on "" / missing.
func removeScrollbackDump(path string) {
	if path == "" {
		return
	}
	_ = os.Remove(path)
	_ = os.Remove(path + ".tmp")
}

// takeScrollbackDump loads and deletes a dump written by the predecessor,
// opened with the one-time key it handed over (hex). A predecessor from
// before sealing passes no key and a plain dump. Always deletes path and
// path+".tmp" so nothing lingers after a corrupt / wrong-version file.
// Missing or unreadable files return nil so restart still comes up (just
// without history).
func takeScrollbackDump(path, keyHex, sessionID string) *scrollbackDump {
	if path == "" {
		return nil
	}
	d := readScrollbackDump(path, func(b []byte) ([]byte, error) {
		if !atrest.IsSealed(b) {
			return b, nil // an older predecessor's plain dump
		}
		key, err := hex.DecodeString(keyHex)
		if err != nil {
			return nil, err
		}
		return atrest.OpenWith(key, kindHandoff, sessionID, b)
	})
	removeScrollbackDump(path)
	return d
}

// readScrollbackDump loads a dump, opened by open, and leaves it in place —
// a restore that fails can be tried again. Nil when missing, unreadable or
// empty.
func readScrollbackDump(path string, open func([]byte) ([]byte, error)) *scrollbackDump {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if body, err = open(body); err != nil {
		return nil
	}
	var dump scrollbackDump
	if json.Unmarshal(body, &dump) != nil || dump.Version != scrollbackDumpVersion {
		return nil
	}
	if len(dump.Entries) == 0 {
		return nil
	}
	return &dump
}

// restoreResumedScrollback re-encrypts a predecessor dump under this agent's
// new session key and replays it into the snapshot emulator. Must run after
// initScreen and before the PTY pump, or live output interleaves with history.
func (a *Agent) restoreResumedScrollback() {
	d := a.resumeDump
	a.resumeDump = nil
	if d == nil || a.buf == nil || a.box == nil {
		return
	}
	entries := make([]scrollEntry, 0, len(d.Entries))
	for _, e := range d.Entries {
		se := scrollEntry{Seq: e.Seq, Bar: e.Bar, Cols: e.Cols, Rows: e.Rows}
		if len(e.Data) > 0 {
			enc, err := a.box.Encrypt(e.Data)
			if err != nil {
				agentNotify("  reminal: could not restore scrollback: %v\n", err)
				return
			}
			se.Data = enc
		}
		entries = append(entries, se)
	}
	a.buf.restore(entries, d.NextSeq, d.BaseCols, d.BaseRows)

	a.screenMu.Lock()
	defer a.screenMu.Unlock()
	if a.screen == nil {
		return
	}
	if d.BaseCols > 0 && d.BaseRows > 0 {
		resizeAnchoredBottom(a.screen, d.BaseCols, d.BaseRows)
		a.noteScrollbackLen()
	}
	for _, e := range d.Entries {
		if e.Cols > 0 && e.Rows > 0 {
			resizeAnchoredBottom(a.screen, e.Cols, e.Rows)
			a.noteScrollbackLen()
			continue
		}
		if len(e.Data) > 0 {
			_, _ = a.screen.Write(e.Data)
			a.noteScrollbackLen()
		}
	}
}
