//go:build !windows

package runner

import "os/exec"

func configureOSProcess(_ *exec.Cmd) {}
