//go:build darwin || freebsd || openbsd || netbsd

package main

import (
	"os"

	"golang.org/x/sys/unix"
)

func termiosSupported() bool {
	_, err := unix.IoctlGetTermios(int(os.Stdin.Fd()), unix.TIOCGETA)
	return err == nil
}

func startConsoleKeyReader() (func(), <-chan byte) {
	ch := make(chan byte, 16)
	fd := int(os.Stdin.Fd())
	orig, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	rawOK := false
	if err == nil {
		raw := *orig
		raw.Lflag &^= unix.ECHO | unix.ICANON
		raw.Cc[unix.VMIN] = 1
		raw.Cc[unix.VTIME] = 0
		if unix.IoctlSetTermios(fd, unix.TIOCSETA, &raw) == nil {
			rawOK = true
		}
	}

	restore := func() {
		if rawOK && orig != nil {
			_ = unix.IoctlSetTermios(fd, unix.TIOCSETA, orig)
		}
	}

	go func() {
		defer close(ch)
		buf := make([]byte, 1)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil || n == 0 {
				return
			}
			ch <- buf[0]
		}
	}()
	return restore, ch
}
