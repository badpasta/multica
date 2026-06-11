//go:build !unix

package backend

import (
	"os/exec"
)

// setProcGroup is a no-op on non-Unix platforms.
func setProcGroup(cmd *exec.Cmd) {}

// killProcGroup falls back to killing just the process on non-Unix platforms.
func killProcGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
