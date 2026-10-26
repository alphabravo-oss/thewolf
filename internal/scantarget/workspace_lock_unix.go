//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package scantarget

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func lockWorkspaceFile(file *os.File, exclusive, nonblocking bool) error {
	mode := unix.LOCK_SH
	if exclusive {
		mode = unix.LOCK_EX
	}
	if nonblocking {
		mode |= unix.LOCK_NB
	}
	err := unix.Flock(int(file.Fd()), mode)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return ErrWorkspaceInUse
	}
	return err
}
