//go:build !windows

package procgroup

import (
	"os/exec"
	"syscall"
)

func setGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// The group, not just the child: a negative pid names the group.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
