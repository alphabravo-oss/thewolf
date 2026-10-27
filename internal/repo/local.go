package repo

import (
	"fmt"
	"os"
	"path/filepath"

	gitpkg "github.com/alphabravocompany/thewolf/internal/gitops"
)

// LocalInfo contains git information about a local repository.
type LocalInfo struct {
	Path          string
	IsGitRepo     bool
	CurrentBranch string
	RemoteURL     string
}

// ValidateLocal checks that a local path exists and gathers git info.
func ValidateLocal(path string) (*LocalInfo, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve path: %w", err)
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return nil, fmt.Errorf("stat path: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", absPath)
	}

	result := &LocalInfo{Path: absPath}

	// Check if it's a git repo
	gitDir := filepath.Join(absPath, ".git")
	if _, err := os.Stat(gitDir); err == nil {
		result.IsGitRepo = true
		result.CurrentBranch = getGitBranch(absPath)
		result.RemoteURL = getGitRemote(absPath)
	}

	return result, nil
}

func getGitBranch(dir string) string {
	branch, err := gitpkg.CurrentBranch(dir)
	if err != nil {
		return "main"
	}
	return branch
}

func getGitRemote(dir string) string {
	return gitpkg.OriginURL(dir)
}
