// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"testing"
	"time"
)

// The first input after a pause is written to the record at once — whatever
// decides by it must hear that someone came back now — and the rest of a
// burst waits for the usual flush.
func TestFirstInputAfterAPauseFlushesAtOnce(t *testing.T) {
	a := &Agent{metaKick: make(chan struct{}, 1)}
	kicked := func() bool {
		select {
		case <-a.metaKick:
			return true
		default:
			return false
		}
	}
	a.markInput()
	if !kicked() {
		t.Fatal("the first input did not ask for a flush")
	}
	a.markInput()
	if kicked() {
		t.Fatal("input in the same burst asked for another flush")
	}
	a.metaMu.Lock()
	a.lastInput = time.Now().Add(-inputFlushAfter)
	a.metaMu.Unlock()
	a.markInput()
	if !kicked() {
		t.Fatal("input after a pause did not ask for a flush")
	}
	if !a.metaDirty.Load() {
		t.Fatal("the record was not marked dirty")
	}
}
