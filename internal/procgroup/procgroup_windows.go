//go:build windows

package procgroup

import "os/exec"

// Windows has no process groups to kill as one; the default Cancel kills
// the child and WaitDelay stops the wait on whatever it left behind.
func setGroup(cmd *exec.Cmd) {}
