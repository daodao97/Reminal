package updater

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestApplyBundleSymlinkSlip proves a malicious bundle can't use a symlink whose
// target escapes staging to write a file to an arbitrary path (symlink tar slip),
// while a legitimate relative in-bundle symlink is preserved.
func TestApplyBundleSymlinkSlip(t *testing.T) {
	parent := t.TempDir()
	appRoot := filepath.Join(parent, "reminal.app")
	if err := os.MkdirAll(filepath.Join(appRoot, "Contents"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A victim directory OUTSIDE the bundle staging. If the slip works, a file
	// lands here; it must not.
	victim := filepath.Join(parent, "victim")
	if err := os.MkdirAll(victim, 0o755); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	write := func(h *tar.Header, body string) {
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(body))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if body != "" {
			if _, err := tw.Write([]byte(body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	write(&tar.Header{Name: "reminal.app/", Typeflag: tar.TypeDir, Mode: 0o755}, "")
	write(&tar.Header{Name: "reminal.app/Contents/", Typeflag: tar.TypeDir, Mode: 0o755}, "")
	// Malicious: absolute symlink escaping to the victim dir…
	write(&tar.Header{Name: "reminal.app/Contents/escape", Typeflag: tar.TypeSymlink, Linkname: victim}, "")
	// …then a regular file written THROUGH it. Without the guard this writes to victim/pwned.
	write(&tar.Header{Name: "reminal.app/Contents/escape/pwned", Typeflag: tar.TypeReg, Mode: 0o644}, "owned")
	// Legit: a relative in-bundle symlink that must survive.
	write(&tar.Header{Name: "reminal.app/Contents/A", Typeflag: tar.TypeReg, Mode: 0o644}, "real")
	write(&tar.Header{Name: "reminal.app/Contents/Current", Typeflag: tar.TypeSymlink, Linkname: "A"}, "")
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	if err := applyBundle(tar.NewReader(&buf), appRoot); err != nil {
		t.Fatalf("applyBundle: %v", err)
	}

	// The slip must NOT have written outside the bundle.
	if _, err := os.Lstat(filepath.Join(victim, "pwned")); err == nil {
		t.Fatal("symlink slip: file was written to the victim dir outside staging")
	}
	// The legit relative symlink must be preserved.
	link, err := os.Readlink(filepath.Join(appRoot, "Contents", "Current"))
	if err != nil {
		t.Fatalf("legit symlink missing: %v", err)
	}
	if link != "A" {
		t.Fatalf("legit symlink target = %q, want %q", link, "A")
	}
}
