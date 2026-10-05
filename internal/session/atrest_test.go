// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"reminal/internal/atrest"
)

func isolateHome(t *testing.T) string {
	t.Helper()
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h)
	return filepath.Join(h, ".reminal")
}

// grepTree fails if any file under dir holds one of needles.
func grepTree(t *testing.T, dir string, needles ...string) {
	t.Helper()
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, _ := os.ReadFile(p)
		for _, n := range needles {
			if strings.Contains(string(b), n) {
				t.Errorf("%s holds %q in the clear", p, n)
			}
		}
		return nil
	})
}

func TestRestoreRecordIsSealed(t *testing.T) {
	dir := isolateHome(t)
	r := Restore{ID: "ABCD2345", PIN: "424242", Token: "tok-SECRET-1", Name: "work", SavedAt: time.Now()}
	if err := WriteRestore(r); err != nil {
		t.Fatal(err)
	}
	grepTree(t, dir, "424242", "tok-SECRET-1")
	got, err := ReadRestore("abcd2345")
	if err != nil || got.PIN != "424242" || got.Token != "tok-SECRET-1" || got.Name != "work" {
		t.Fatalf("read back %+v, %v", got, err)
	}
	all, err := ReadRestores()
	if err != nil || len(all) != 1 {
		t.Fatalf("ReadRestores = %v, %v", all, err)
	}
	_ = ClearRestore("ABCD2345")
	if all, _ := ReadRestores(); len(all) != 0 {
		t.Fatal("ClearRestore left the record")
	}
}

// A tree an older version wrote — plain record, plain scrollback — is
// sealed on first read and the plain copies are gone.
func TestLegacyRestoreMigrates(t *testing.T) {
	dir := isolateHome(t)
	rd := filepath.Join(dir, "restore")
	_ = os.MkdirAll(rd, 0o700)
	plain, _ := json.Marshal(Restore{ID: "OLDS2345", PIN: "135791", Token: "tok-OLD-2", SavedAt: time.Now()})
	_ = os.WriteFile(filepath.Join(rd, "OLDS2345.json"), plain, 0o600)
	_ = os.WriteFile(filepath.Join(rd, "OLDS2345.scrollback.json"), []byte(`{"v":1,"entries":[{"seq":1,"data":"TUFSS0VSLXR5cGVk"}],"marker":"MARKER-typed"}`), 0o600)

	all, err := ReadRestores()
	if err != nil || len(all) != 1 || all[0].PIN != "135791" {
		t.Fatalf("ReadRestores = %+v, %v", all, err)
	}
	for _, n := range []string{"OLDS2345.json", "OLDS2345.scrollback.json"} {
		if _, err := os.Stat(filepath.Join(rd, n)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("plain %s still there", n)
		}
	}
	grepTree(t, dir, "135791", "tok-OLD-2", "MARKER-typed", "TUFSS0VSLXR5cGVk")
	sp, _ := RestoreScrollbackPath("OLDS2345")
	blob, err := os.ReadFile(sp)
	if err != nil {
		t.Fatal(err)
	}
	pt, err := OpenScrollback("OLDS2345", blob)
	if err != nil || !strings.Contains(string(pt), "MARKER-typed") {
		t.Fatalf("migrated scrollback = %q, %v", pt, err)
	}
}

// The key removed after a seal: the record is quarantined, not deleted, and
// reads as missing so the session starts fresh.
func TestKeyGoneQuarantinesNotDeletes(t *testing.T) {
	dir := isolateHome(t)
	_ = WriteRestore(Restore{ID: "GONE2345", PIN: "111111", SavedAt: time.Now()})
	sp, _ := RestoreScrollbackPath("GONE2345")
	blob, _ := SealScrollback("GONE2345", []byte("history"))
	_ = os.WriteFile(sp, blob, 0o600)
	// The key FILE missing is damage: later, nothing quarantined.
	if err := os.Remove(filepath.Join(dir, "atrest.key")); err != nil {
		t.Fatal(err)
	}
	resetAtrestCache(t)
	if _, err := ReadRestore("GONE2345"); !errors.Is(err, atrest.ErrLocked) {
		t.Fatalf("read with the current key file missing: %v, want ErrLocked", err)
	}
	if _, err := os.ReadDir(filepath.Join(dir, "restore", "quarantine")); err == nil {
		t.Fatal("quarantined while the current key was merely missing")
	}
	// A real rotation (atrest.json names another key): gone, quarantined.
	atrest.RotateForTest()
	if _, err := ReadRestore("GONE2345"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read with key rotated away: %v", err)
	}
	q, _ := os.ReadDir(filepath.Join(dir, "restore", "quarantine"))
	var names []string
	for _, e := range q {
		names = append(names, e.Name())
	}
	j := strings.Join(names, " ")
	if !strings.Contains(j, "GONE2345.sealed") || !strings.Contains(j, "GONE2345.scrollback.sealed") || !strings.Contains(j, ".reason") {
		t.Fatalf("quarantine holds %v", names)
	}
	if QuarantinedRestores() != 1 {
		t.Fatal("quarantine count")
	}
	if all, _ := ReadRestores(); len(all) != 0 {
		t.Fatal("quarantined session still listed")
	}
}

func TestActivePINSealedOnDisk(t *testing.T) {
	dir := isolateHome(t)
	a := Active{ID: "ACTV2345", PIN: "987654", PID: os.Getpid(), StartedAt: time.Now(), PidStartedAt: SelfStartTime()}
	if err := WriteActive(a); err != nil {
		t.Fatal(err)
	}
	grepTree(t, dir, "987654")
	got, err := ReadActiveByID("ACTV2345")
	if err != nil || got.PIN != "987654" {
		t.Fatalf("read back %+v %v", got, err)
	}
	// A record an older version wrote keeps working.
	old, _ := json.Marshal(Active{ID: "OLDA2345", PIN: "246802", PID: os.Getpid(), StartedAt: time.Now()})
	_ = os.WriteFile(filepath.Join(dir, "active-OLDA2345.json"), old, 0o600)
	got, err = ReadActiveByID("OLDA2345")
	if err != nil || got.PIN != "246802" {
		t.Fatalf("old record %+v %v", got, err)
	}
}

// A session still running the old binary after an upgrade keeps rewriting its
// plain record: it is read (the newer copy wins over a stale sealed one) but
// not sealed, so its own cleanup on exit still removes everything.
func TestLiveOldSessionNotMigrated(t *testing.T) {
	dir := isolateHome(t)
	rd := filepath.Join(dir, "restore")
	_ = os.MkdirAll(rd, 0o700)
	_ = WriteRestore(Restore{ID: "LIVE2345", PIN: "000001", Name: "stale", SavedAt: time.Now()})
	time.Sleep(20 * time.Millisecond)
	plain, _ := json.Marshal(Restore{ID: "LIVE2345", PIN: "000001", Name: "fresh", SavedAt: time.Now()})
	_ = os.WriteFile(filepath.Join(rd, "LIVE2345.json"), plain, 0o600)
	act, _ := json.Marshal(Active{ID: "LIVE2345", PID: os.Getpid(), StartedAt: time.Now()})
	_ = os.WriteFile(filepath.Join(dir, "active-LIVE2345.json"), act, 0o600)

	r, err := ReadRestore("LIVE2345")
	if err != nil || r.Name != "fresh" {
		t.Fatalf("read %+v %v, want the newer plain record", r, err)
	}
	if _, err := os.Stat(filepath.Join(rd, "LIVE2345.json")); err != nil {
		t.Fatal("a live session's plain record was migrated away")
	}
	// Once it is not running, the next read seals it.
	_ = os.Remove(filepath.Join(dir, "active-LIVE2345.json"))
	if r, _ := ReadRestore("LIVE2345"); r == nil || r.Name != "fresh" {
		t.Fatalf("after exit: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(rd, "LIVE2345.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("plain record kept after its session stopped running")
	}
	if r, _ := ReadRestore("LIVE2345"); r == nil || r.Name != "fresh" {
		t.Fatalf("sealed copy: %+v", r)
	}
}

// Several processes migrating the same old records at once (the daemon at
// start while someone runs `reminal restore`) lose nothing: every record ends
// up sealed, readable, with no plain copy and nothing quarantined.
func TestParallelMigrationLosesNothing(t *testing.T) {
	if os.Getenv("REMINAL_MIGRATE_HELPER") == "1" {
		_, _ = ReadRestores()
		return
	}
	dir := isolateHome(t)
	rd := filepath.Join(dir, "restore")
	_ = os.MkdirAll(rd, 0o700)
	const n = 120
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("M%07d", i)
		plain, _ := json.Marshal(Restore{ID: ids[i], PIN: fmt.Sprintf("%06d", i), SavedAt: time.Now()})
		_ = os.WriteFile(filepath.Join(rd, ids[i]+".json"), plain, 0o600)
		_ = os.WriteFile(filepath.Join(rd, ids[i]+".scrollback.json"), []byte(`{"v":1}`), 0o600)
	}
	// Seal once first so every process shares one key (as on a real machine
	// whose key exists before the upgrade's first start).
	if _, err := SealScrollback("WARMUP00", []byte("x")); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for p := 0; p < 4; p++ { // separate processes
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.Command(os.Args[0], "-test.run=^TestParallelMigrationLosesNothing$")
			cmd.Env = append(os.Environ(), "REMINAL_MIGRATE_HELPER=1")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("helper: %v %s", err, out)
			}
		}()
	}
	for g := 0; g < 4; g++ { // and goroutines in this one
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = ReadRestores() }()
	}
	wg.Wait()
	resetAtrestCache(t)
	for i, id := range ids {
		if _, err := os.Stat(filepath.Join(rd, id+".json")); err == nil {
			t.Errorf("%s: plain record left", id)
		}
		r, err := ReadRestore(id)
		if err != nil || r.PIN != fmt.Sprintf("%06d", i) {
			t.Errorf("%s: lost (%v)", id, err)
		}
	}
	if q, _ := os.ReadDir(filepath.Join(rd, "quarantine")); len(q) > 0 {
		t.Errorf("%d files quarantined", len(q))
	}
}
