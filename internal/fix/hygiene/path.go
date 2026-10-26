package hygiene

import (
	"os"
	"path/filepath"
)

// openRepoFile confines finding-supplied paths to the remediation workspace.
// os.Root also checks symlinks at the actual read/write, closing traversal races.
func openRepoFile(repoPath, name string) (*os.Root, string, error) {
	if repoPath == "" {
		return nil, "", os.ErrInvalid
	}
	if filepath.IsAbs(name) {
		absolute, err := filepath.Abs(repoPath)
		if err != nil {
			return nil, "", err
		}
		name, err = filepath.Rel(absolute, name)
		if err != nil {
			return nil, "", err
		}
	}
	if !filepath.IsLocal(name) || ProtectedRel(name) {
		return nil, "", os.ErrPermission
	}
	root, err := os.OpenRoot(repoPath)
	return root, name, err
}
