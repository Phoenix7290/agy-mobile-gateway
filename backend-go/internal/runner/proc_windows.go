//go:build windows

package runner

import (
	"os/exec"
	"syscall"
)

// configureOSProcess hides the console window on Windows standalone runs (CREATE_NO_WINDOW).
func configureOSProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
}
