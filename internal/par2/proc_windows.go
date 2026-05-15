//go:build windows

package par2

import (
	"os/exec"
)

// setPgid is a no-op on Windows.
func setPgid(cmd *exec.Cmd) {
	// Windows doesn't support process groups the same way
}

// killProcessGroup kills the process on Windows.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		cmd.Process.Kill()
	}
}
