//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !windows

package scantarget

import (
	"errors"
	"os"
)

func lockWorkspaceFile(_ *os.File, _ bool, _ bool) error {
	return errors.New("managed workspace locking is unsupported on this platform")
}
