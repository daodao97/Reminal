// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

package client

import (
	"net"
	"os"
	"testing"
	"time"
)

// A hot restart closes the control socket before exec'ing, to free the path for
// the successor. When the exec fails the successor never arrives, and the
// session is left streaming but unreachable: restart, stop and kill all reach a
// session through that socket, so the failure notice's advice to try again
// would have been impossible to follow. The listener has to come back.
func TestControlListenerRebindsAfterTeardown(t *testing.T) {
	path, err := controlSockPath(os.Getpid())
	if err != nil {
		t.Skipf("no control socket path here: %v", err)
	}
	a := &Agent{}

	stop := a.listenControl()
	if !controlSocketAnswers(t, path) {
		t.Fatal("listener did not answer after starting")
	}

	stop() // what the restart tear-down does
	if controlSocketAnswers(t, path) {
		t.Fatal("listener still answering after tear-down")
	}

	// The recovery path: bind the same address again.
	stop2 := a.listenControl()
	defer stop2()
	if !controlSocketAnswers(t, path) {
		t.Fatal("listener did not come back — a failed restart would strand the session unreachable")
	}
}

func controlSocketAnswers(t *testing.T, path string) bool {
	t.Helper()
	c, err := net.DialTimeout("unix", path, 500*time.Millisecond)
	if err != nil {
		return false
	}
	defer c.Close()
	// A dial alone can succeed against a stale socket file; require the
	// listener to actually be accepting by completing a write.
	_ = c.SetDeadline(time.Now().Add(500 * time.Millisecond))
	_, err = c.Write([]byte("\n"))
	return err == nil
}
