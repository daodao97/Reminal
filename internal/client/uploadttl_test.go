// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// uploadHome points HOME at a temp dir and returns the uploads directory, so
// these tests never touch the real ~/Downloads.
func uploadHome(t *testing.T) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	dir, err := uploadsDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func uploadedFile(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// The promise an upload makes — "auto-delete in 1h" — outlives the session
// that made it. The deadline is recorded, so whatever runs next keeps it.
func TestSweepDeletesUploadsWhoseDeadlinePassed(t *testing.T) {
	dir := uploadHome(t)
	expired := uploadedFile(t, dir, "expired.bin")
	future := uploadedFile(t, dir, "future.bin")

	rememberUploadTTL(expired, time.Now().Add(-time.Minute))
	rememberUploadTTL(future, time.Now().Add(time.Hour))

	if n := sweepUploadTTLs(time.Now()); n != 1 {
		t.Fatalf("swept %d files, want 1", n)
	}
	if _, err := os.Stat(expired); !os.IsNotExist(err) {
		t.Errorf("expired upload still on disk: %v", err)
	}
	if _, err := os.Stat(future); err != nil {
		t.Errorf("upload that is still wanted was deleted: %v", err)
	}
	// The one still owed stays on the record; the deleted one is gone from it.
	left := readUploadTTLs()
	if len(left) != 1 || left[0].Path != future {
		t.Errorf("record after sweep = %+v, want only %s", left, future)
	}
}

// A second sweep must not keep trying, or report work it did not do.
func TestSweepIsIdempotent(t *testing.T) {
	dir := uploadHome(t)
	p := uploadedFile(t, dir, "once.bin")
	rememberUploadTTL(p, time.Now().Add(-time.Second))

	if n := sweepUploadTTLs(time.Now()); n != 1 {
		t.Fatalf("first sweep deleted %d, want 1", n)
	}
	if n := sweepUploadTTLs(time.Now()); n != 0 {
		t.Fatalf("second sweep deleted %d, want 0", n)
	}
	if left := readUploadTTLs(); len(left) != 0 {
		t.Errorf("record not empty after everything was deleted: %+v", left)
	}
}

// Someone clearing out their Downloads themselves is not a failure, and must
// not leave the record growing forever.
func TestSweepForgetsFilesAlreadyGone(t *testing.T) {
	dir := uploadHome(t)
	p := filepath.Join(dir, "never-existed.bin")
	rememberUploadTTL(p, time.Now().Add(-time.Second))

	if n := sweepUploadTTLs(time.Now()); n != 0 {
		t.Fatalf("deleted %d files that do not exist", n)
	}
	if left := readUploadTTLs(); len(left) != 0 {
		t.Errorf("record kept an entry for a file that is gone: %+v", left)
	}
}

// The record says which files to delete, so it decides what gets removed from
// someone's disk. A path outside the uploads directory — a corrupt record, a
// stale one, or one written by something else — must never be acted on.
func TestSweepRefusesPathsOutsideTheUploadsDirectory(t *testing.T) {
	uploadHome(t)
	home, _ := os.UserHomeDir()
	outside := filepath.Join(home, "precious.txt")
	if err := os.WriteFile(outside, []byte("not an upload"), 0o644); err != nil {
		t.Fatal(err)
	}
	rememberUploadTTL(outside, time.Now().Add(-time.Hour))

	if n := sweepUploadTTLs(time.Now()); n != 0 {
		t.Fatalf("deleted %d files outside the uploads directory", n)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("a file outside the uploads directory was deleted: %v", err)
	}
	if left := readUploadTTLs(); len(left) != 0 {
		t.Errorf("refused entry kept on the record: %+v", left)
	}
}

// A symlink in the uploads directory points somewhere else; deleting through
// one would remove a file nobody uploaded.
func TestSweepRefusesSymlinks(t *testing.T) {
	dir := uploadHome(t)
	home, _ := os.UserHomeDir()
	target := filepath.Join(home, "target.txt")
	if err := os.WriteFile(target, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.bin")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	rememberUploadTTL(link, time.Now().Add(-time.Hour))

	if n := sweepUploadTTLs(time.Now()); n != 0 {
		t.Fatalf("deleted %d symlinked files", n)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("symlink target was deleted: %v", err)
	}
}

// Recording the same file twice (a re-upload to the same resolved path) keeps
// one entry, with the newer deadline.
func TestRememberReplacesAnEarlierDeadline(t *testing.T) {
	dir := uploadHome(t)
	p := uploadedFile(t, dir, "again.bin")
	rememberUploadTTL(p, time.Now().Add(-time.Hour))
	rememberUploadTTL(p, time.Now().Add(time.Hour))

	if n := sweepUploadTTLs(time.Now()); n != 0 {
		t.Fatalf("deleted a file whose deadline was moved out: %d", n)
	}
	if left := readUploadTTLs(); len(left) != 1 {
		t.Fatalf("record = %+v, want one entry", left)
	}
}

// forgetUploadTTL is what the session's own timer calls once it has deleted
// the file; the record must not keep the entry after that.
func TestForgetDropsTheEntry(t *testing.T) {
	dir := uploadHome(t)
	p := uploadedFile(t, dir, "timer.bin")
	rememberUploadTTL(p, time.Now().Add(time.Hour))
	forgetUploadTTL(p)
	if left := readUploadTTLs(); len(left) != 0 {
		t.Fatalf("record = %+v, want empty", left)
	}
}
