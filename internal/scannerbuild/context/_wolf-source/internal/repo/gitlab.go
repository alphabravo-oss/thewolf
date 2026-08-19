package repo

import (
	"fmt"
	"path/filepath"

	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
)

// CloneGitLab clones a GitLab repository to the local cache.
func CloneGitLab(group, repo, branch, token string) (string, error) {
	cacheDir, err := getCacheDir()
	if err != nil {
		return "", err
	}

	dest := filepath.Join(cacheDir, "gitlab", group, repo)
	url := fmt.Sprintf("https://gitlab.com/%s/%s.git", group, repo)
	var auth transport.AuthMethod
	if token != "" {
		auth = &http.BasicAuth{Username: "oauth2", Password: token}
	}
	return cloneOrRefresh(dest, url, branch, auth)
}
