// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"bytes"
	"testing"
)

// FuzzWinHelperReadLoop feeds arbitrary bytes to the [uint32 big-endian len][JPEG]
// frame decoder that winHelper.readLoop uses to read frames from reminal-capture /
// the daemon mirror socket. A buggy or corrupt helper shouldn't crash the agent:
// the decoder must never panic, over-allocate (the 16 MiB length cap), or hang
// (a finite reader must always reach EOF and close h.dead).
func FuzzWinHelperReadLoop(f *testing.F) {
	seeds := [][]byte{
		{0, 0, 0, 3, 1, 2, 3},      // len=3, exactly 3 bytes
		{0, 0, 0, 0},               // len=0 (rejected)
		{0xFF, 0xFF, 0xFF, 0xFF},   // len ~4 GiB (> cap, rejected)
		{0, 0, 0, 5, 1, 2},         // len=5 but only 2 bytes -> short read -> EOF
		{},                         // empty
		{0, 0, 0, 2, 9, 9, 0, 0, 0, 1, 7}, // two frames back-to-back
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		h := &winHelper{
			sig:  make(chan struct{}, 1), // non-blocking send in readLoop; no drain needed
			dead: make(chan struct{}),
		}
		h.readLoop(bytes.NewReader(data)) // finite reader -> EOF -> returns, closes dead
		select {
		case <-h.dead: // readLoop terminated as required
		default:
			t.Fatal("readLoop returned without closing dead")
		}
	})
}
