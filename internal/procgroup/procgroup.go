// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 Harshal Gajjar

// Package procgroup bounds a child process and everything it starts. A shell
// run for its PATH, or an agent's own `mcp add`, may leave children behind
// that hold the pipes open; without this, a timeout kills the child and then
// waits on the grandchildren forever.
package procgroup

import (
	"os/exec"
	"time"
)

// Bound makes cmd's timeout real: the child runs in its own process group,
// cancelling the context kills the whole group, and Wait gives up on the
// pipes a moment after the child is gone.
func Bound(cmd *exec.Cmd) {
	setGroup(cmd)
	cmd.WaitDelay = time.Second
}
