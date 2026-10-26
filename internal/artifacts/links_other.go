//go:build !unix && !windows

package artifacts

import "os"

// Unsupported platforms cannot establish the artifact's link boundary.
func singleLink(_ *os.File, _ os.FileInfo) bool { return false }
