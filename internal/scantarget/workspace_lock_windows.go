package scantarget

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func lockWorkspaceFile(file *os.File, exclusive, nonblocking bool) error {
	var flags uint32
	if exclusive {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	if nonblocking {
		flags |= windows.LOCKFILE_FAIL_IMMEDIATELY
	}
	err := windows.LockFileEx(windows.Handle(file.Fd()), flags, 0, 1, 0, &windows.Overlapped{})
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return ErrWorkspaceInUse
	}
	return err
}
