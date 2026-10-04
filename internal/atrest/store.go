// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package atrest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"reminal/internal/atomicfile"
)

// keystoreService names the key in an OS keystore. The account is a hash of
// the ~/.reminal path, so each home (and each user) has its own item and a
// throwaway HOME can never read or overwrite the person's own.
const keystoreService = "reminal-at-rest-key"

func keystoreAccount(dir string) string {
	s := sha256.Sum256([]byte(filepath.Clean(dir)))
	return "home-" + hex.EncodeToString(s[:8])
}

// fileStore is the fallback: the key in a 0600 file beside what it seals.
type fileStore struct{ dir string }

func (fileStore) name() string   { return "file" }
func (fileStore) source() byte   { return srcFile }
func (f fileStore) path() string { return filepath.Join(f.dir, "atrest.key") }

func (f fileStore) get() ([]byte, error) {
	b, err := os.ReadFile(f.path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, ErrLocked
	}
	k, err := hex.DecodeString(strings.TrimSpace(string(b)))
	if err != nil || len(k) != keyLen {
		return nil, errNotFound
	}
	return k, nil
}

func (f fileStore) put(k []byte) error {
	return atomicfile.Write(f.path(), []byte(hex.EncodeToString(k)+"\n"), 0o600)
}

// runTool runs a keystore helper with a hard timeout. A timeout, a missing
// binary, anything but a clean exit comes back as an error with the exit code
// (-1 when it never exited on its own).
func runTool(stdin []byte, name string, args ...string) (stdout, stderr []byte, code int, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), keystoreTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	// A helper that forks and leaves a child holding our pipes must not
	// keep us waiting past the timeout.
	cmd.WaitDelay = 500 * time.Millisecond
	err = cmd.Run()
	if ctx.Err() != nil {
		return out.Bytes(), errb.Bytes(), -1, ErrLocked
	}
	code = 0
	if err != nil {
		code = -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
	}
	return out.Bytes(), errb.Bytes(), code, err
}
