// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reminal/internal/config"
)

func TestCopyCodeShape(t *testing.T) {
	for i := 0; i < 200; i++ {
		c, err := generateCode()
		if err != nil {
			t.Fatal(err)
		}
		if len(c) != codeLen {
			t.Fatalf("code %q is %d characters, want %d", c, len(c), codeLen)
		}
		for _, r := range c {
			if !strings.ContainsRune(codeAlphabet, r) {
				t.Fatalf("code %q has %q, outside the alphabet", c, r)
			}
		}
	}
	if got := displayCode("ABCDEFGHJK"); got != "ABCDE-FGHJK" {
		t.Fatalf("displayCode = %q, want ABCDE-FGHJK", got)
	}
	if got := normalizeCode(" abcde-fghjk "); got != "ABCDEFGHJK" {
		t.Fatalf("normalizeCode = %q", got)
	}
}

// The relay is given where the two ends meet, and nothing more.
func TestRelayIsOnlyGivenTheRoutingHalf(t *testing.T) {
	code := "ABCDEFGHJK"
	if routeID(code) != "ABCDE" {
		t.Fatalf("routeID = %q, want the first %d characters", routeID(code), routeLen)
	}
	for _, role := range []string{"source", "paste"} {
		u := config.RendezvousWS(routeID(code), role)
		if strings.Contains(strings.ToUpper(u), "FGHJK") {
			t.Fatalf("relay URL %q carries the second half of the code", u)
		}
	}
}

// Two ends that agree on where to meet but not on the second half must not
// complete a transfer: the second half is what authenticates it.
func TestSameRoutingHalfDifferentSecretHalfFails(t *testing.T) {
	srcDir := t.TempDir()
	srcFile := filepath.Join(srcDir, "file.bin")
	if err := os.WriteFile(srcFile, []byte("contents"), 0o644); err != nil {
		t.Fatal(err)
	}
	dstDir := t.TempDir()

	srcConn, pasteConn := newMemPair()
	srcErr := make(chan error, 1)
	go func() { srcErr <- runSource(srcConn, "ABCDEFGHJK", srcFile) }()

	// Bounded: if the second half ever stopped mattering, the two ends would
	// agree and the transfer would carry on — this must report that, not hang.
	pasteErr := make(chan error, 1)
	go func() {
		_, err := runPaste(pasteConn, "ABCDEFGHJM", dstDir) // same first five
		pasteErr <- err
	}()
	select {
	case err := <-pasteErr:
		if err != errWrongCode {
			t.Fatalf("paste error = %v, want errWrongCode — the second half did not decide the transfer", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the paste neither failed nor finished: the two ends agreed on a key without the second half")
	}
	close(pasteConn.out)
	select {
	case serr := <-srcErr:
		if serr == nil {
			t.Fatal("the source finished a transfer to a paste with the wrong second half")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the source never gave up on a paste with the wrong second half")
	}
	if entries, _ := os.ReadDir(dstDir); len(entries) != 0 {
		t.Fatalf("a file was written: %v", entries)
	}
}

// A code from before the split cannot be met by this version; say so plainly
// instead of reporting it as wrong or expired.
func TestLegacyCodeIsExplained(t *testing.T) {
	err := RunPaste("ABCD-EFGH", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "older reminal") {
		t.Fatalf("RunPaste(8-character code) = %v, want an explanation that the copier is older", err)
	}
}
