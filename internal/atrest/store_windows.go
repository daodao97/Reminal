// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package atrest

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"reminal/internal/atomicfile"
)

const caseInsensitiveFS = true

// dpapiStore keeps the key wrapped by DPAPI for the current user: the blob in
// ~/.reminal/atrest.key.dpapi opens only for this Windows account. The daemon
// runs as the user (an HKCU Run key), so it shares the CLI's DPAPI scope.
type dpapiStore struct{ dir, file string }

// osStore: the account's suffix after a dot (a key kept by id, see
// promoteFallback) picks its own blob file.
func osStore(dir, account string) store {
	name := "atrest.key.dpapi"
	if i := strings.LastIndexByte(account, '.'); i >= 0 {
		name = "atrest-" + account[i+1:] + ".key.dpapi"
	}
	return dpapiStore{dir: dir, file: name}
}

func (dpapiStore) name() string   { return "dpapi" }
func (dpapiStore) source() byte   { return srcDPAPI }
func (d dpapiStore) path() string { return filepath.Join(d.dir, d.file) }

var dpapiEntropy = []byte("reminal at-rest key")

func blobOf(b []byte) *windows.DataBlob {
	if len(b) == 0 {
		return &windows.DataBlob{}
	}
	return &windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
}

func takeBlob(out *windows.DataBlob) []byte {
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...)
}

// bounded runs f with the keystore timeout: DPAPI is an in-process call, but
// it can wait on the profile service.
func bounded(f func() ([]byte, error)) ([]byte, error) {
	type res struct {
		b   []byte
		err error
	}
	ch := make(chan res, 1)
	go func() { b, err := f(); ch <- res{b, err} }()
	select {
	case r := <-ch:
		return r.b, r.err
	case <-time.After(keystoreTimeout):
		return nil, ErrLocked
	}
}

func (d dpapiStore) get() ([]byte, error) {
	blob, err := os.ReadFile(d.path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, ErrLocked
	}
	k, err := bounded(func() ([]byte, error) {
		var out windows.DataBlob
		if err := windows.CryptUnprotectData(blobOf(blob), nil, blobOf(dpapiEntropy), 0, nil,
			windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
			return nil, err
		}
		return takeBlob(&out), nil
	})
	if err != nil || len(k) != keyLen {
		// DPAPI refusing is ambiguous (a profile repair, a roaming
		// profile not loaded yet): locked, never gone.
		return nil, ErrLocked
	}
	return k, nil
}

func (d dpapiStore) put(k []byte) error { return d.write(k, false) }

// putNew writes the blob only if none exists: errExists otherwise.
func (d dpapiStore) putNew(k []byte) error { return d.write(k, true) }

func (d dpapiStore) write(k []byte, create bool) error {
	blob, err := bounded(func() ([]byte, error) {
		var out windows.DataBlob
		if err := windows.CryptProtectData(blobOf(k), nil, blobOf(dpapiEntropy), 0, nil,
			windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
			return nil, err
		}
		return takeBlob(&out), nil
	})
	if err != nil {
		return err
	}
	if create {
		if err := atomicfile.WriteNew(d.path(), blob, 0o600); errors.Is(err, atomicfile.ErrExists) {
			return errExists
		} else {
			return err
		}
	}
	return atomicfile.Write(d.path(), blob, 0o600)
}

// hasDesktopKeystore: a real login user here is expected to have an OS keystore.
const hasDesktopKeystore = true
