// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package updater

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestMigrateBareToBundle proves the bare→.app upgrade: a loose-binary install
// (pre-bundle `curl install.sh`) upgrading to a modern darwin release lands the
// signed reminal.app under REMINAL_APP_DIR, repoints the on-PATH CLI at it via a
// symlink (so `reminal` keeps resolving and the daemon runs the sh.reminal
// identity), and drops the stale loose capture helper. Keyed on install STATE, not
// version, so it self-repairs on any future version→bundle transition.
func TestMigrateBareToBundle(t *testing.T) {
	tmp := t.TempDir()

	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bareBin := filepath.Join(binDir, "reminal")
	if err := os.WriteFile(bareBin, []byte("#!old bare binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A stale loose helper from the old bare install; the migration must remove it
	// so it can't shadow the bundle's own copy.
	looseHelper := filepath.Join(binDir, "reminal-capture")
	if err := os.WriteFile(looseHelper, []byte("old helper"), 0o755); err != nil {
		t.Fatal(err)
	}

	appDirPath := filepath.Join(tmp, "Applications")
	t.Setenv("REMINAL_APP_DIR", appDirPath)

	// Minimal reminal.app tar (dirs + Info.plist + the two Mach-O stand-ins).
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	dir := func(name string) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
			t.Fatal(err)
		}
	}
	file := func(name, body string, mode int64) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: mode, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	dir("reminal.app/")
	dir("reminal.app/Contents/")
	dir("reminal.app/Contents/MacOS/")
	file("reminal.app/Contents/Info.plist", "<plist/>", 0o644)
	file("reminal.app/Contents/MacOS/reminal", "#!new bundle binary", 0o755)
	file("reminal.app/Contents/MacOS/reminal-capture", "#!bundle helper", 0o755)
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	if err := migrateBareToBundle(tar.NewReader(&buf), bareBin); err != nil {
		t.Fatalf("migrateBareToBundle: %v", err)
	}

	// The bundle landed under REMINAL_APP_DIR.
	inner := filepath.Join(appDirPath, "reminal.app", "Contents", "MacOS", "reminal")
	if _, err := os.Stat(inner); err != nil {
		t.Fatalf("bundle binary not installed: %v", err)
	}

	// The old bare CLI path is now a symlink INTO the bundle.
	fi, err := os.Lstat(bareBin)
	if err != nil {
		t.Fatalf("lstat bareBin: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("bareBin is not a symlink after migration (mode %v)", fi.Mode())
	}
	dest, err := os.Readlink(bareBin)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if dest != inner {
		t.Fatalf("CLI symlink points at %q, want %q", dest, inner)
	}

	// The stale loose helper was removed.
	if _, err := os.Stat(looseHelper); !os.IsNotExist(err) {
		t.Fatalf("stale loose reminal-capture not removed (stat err=%v)", err)
	}
}

// TestApplyBundleFreshInstall proves applyBundle works when there is NO existing
// bundle to replace (the bare→bundle case), not just the in-place swap path.
func TestApplyBundleFreshInstall(t *testing.T) {
	tmp := t.TempDir()
	appRoot := filepath.Join(tmp, "reminal.app") // does not exist yet

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Name: "reminal.app/", Typeflag: tar.TypeDir, Mode: 0o755})
	_ = tw.WriteHeader(&tar.Header{Name: "reminal.app/Contents/", Typeflag: tar.TypeDir, Mode: 0o755})
	body := "binary"
	_ = tw.WriteHeader(&tar.Header{Name: "reminal.app/Contents/x", Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(body))})
	_, _ = tw.Write([]byte(body))
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	if err := applyBundle(tar.NewReader(&buf), appRoot); err != nil {
		t.Fatalf("applyBundle fresh: %v", err)
	}
	if _, err := os.Stat(filepath.Join(appRoot, "Contents", "x")); err != nil {
		t.Fatalf("fresh bundle not installed: %v", err)
	}
}
