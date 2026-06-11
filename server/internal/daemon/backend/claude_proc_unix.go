//go:build unix

package backend

import (
	"os/exec"
	"syscall"
)

// setProcGroup configures the command to run in its own process group so
// that context cancellation can kill the entire group (including any child
// processes the CLI spawns) rather than just the top-level process.
func setProcGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killProcGroup sends SIGKILL to the process group of cmd. Falls back to
// killing just the process if the group kill fails (e.g. process already
// exited).
func killProcGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		// Negative PID kills the entire process group.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
