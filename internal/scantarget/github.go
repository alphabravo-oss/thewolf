package scantarget

import (
	"context"
	"fmt"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
	repocache "github.com/alphabravocompany/thewolf/internal/repo"
	"github.com/alphabravocompany/thewolf/internal/secrets"
)

// prepareGitHub resolves a GitHub-sourced repo into a usable local path. It
// parses the SourcePath into owner/name, looks up the caller's optional
// github_token secret, and clones (or refreshes) the repo into the wolf
// cache. Tests inject GitHubCloner to avoid hitting the network.
func (r Resolver) prepareGitHub(ctx context.Context, repo *models.Repo, branch string) (Prepared, error) {
	owner, name, err := ParseGitHubSource(repo.SourcePath)
	if err != nil {
		return Prepared{}, err
	}
	if branch == "" {
		branch = repo.DefaultBranch
	}

	token, err := r.lookupGitHubToken(ctx, repo)
	if err != nil {
		return Prepared{}, fmt.Errorf("github token lookup: %w", err)
	}

	cloner := r.GitHubCloner
	if cloner == nil {
		cloner = repocache.CloneGitHub
	}
	path, err := cloner(owner, name, branch, token)
	if err != nil {
		// Be deliberately vague — the token is private and a wrong-token
		// vs missing-repo distinction would leak whether the repo exists.
		return Prepared{}, fmt.Errorf("clone github.com/%s/%s: %w", owner, name, err)
	}

	sha, dirty := localGitState(path)
	return Prepared{
		Path:              path,
		SourceType:        models.SourceTypeGitHub,
		SourcePath:        repo.SourcePath,
		CommitSHA:         sha,
		DirtyState:        dirty,
		PreparedWorkspace: path,
		// The clone lives in the configured workspace cache and is refreshed in
		// place on the next scan, so cleanup is a no-op.
		Cleanup: func() {},
	}, nil
}

// ParseGitHubSource accepts any of the common ways a user might describe a
// GitHub repository and returns the owner and name:
//
//	owner/repo
//	github.com/owner/repo
//	https://github.com/owner/repo
//	https://github.com/owner/repo.git
//	git@github.com:owner/repo.git
func ParseGitHubSource(raw string) (owner, name string, err error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", "", fmt.Errorf("github source is empty")
	}

	// Strip URL scheme and host so we land on "owner/repo[.git]".
	switch {
	case strings.HasPrefix(s, "git@github.com:"):
		s = strings.TrimPrefix(s, "git@github.com:")
	case strings.HasPrefix(s, "https://github.com/"):
		s = strings.TrimPrefix(s, "https://github.com/")
	case strings.HasPrefix(s, "http://github.com/"):
		s = strings.TrimPrefix(s, "http://github.com/")
	case strings.HasPrefix(s, "github.com/"):
		s = strings.TrimPrefix(s, "github.com/")
	}
	s = strings.TrimSuffix(s, ".git")
	s = strings.Trim(s, "/")

	parts := strings.Split(s, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("github source %q must be owner/repo (or a github.com URL)", raw)
	}
	if strings.ContainsAny(parts[0]+parts[1], " \t\n@:") {
		return "", "", fmt.Errorf("github source %q contains invalid characters", raw)
	}
	return parts[0], parts[1], nil
}

// lookupGitHubToken returns the repo's selected github_token secret when one
// is configured, otherwise the first github_token secret owned by the user.
// An empty string with no error means the user has no token and the clone
// should proceed unauthenticated (public-repo path).
func (r Resolver) lookupGitHubToken(ctx context.Context, repo *models.Repo) (string, error) {
	if r.Store == nil || repo == nil || repo.UserID == "" {
		return "", nil
	}
	if repo.CredentialSecretID != "" {
		s, err := r.Store.GetSecretByID(ctx, repo.CredentialSecretID)
		if err != nil || s.UserID != repo.UserID {
			return "", fmt.Errorf("selected github token was not found")
		}
		if s.KeyType != models.KeyTypeGitHubToken {
			return "", fmt.Errorf("selected credential is not a github_token")
		}
		plaintext, derr := secrets.Decrypt(s.EncryptedValue)
		if derr != nil {
			return "", fmt.Errorf("decrypt github token %q: %w", s.KeyName, derr)
		}
		return plaintext, nil
	}
	list, err := r.Store.ListSecretsByUser(ctx, repo.UserID)
	if err != nil {
		return "", err
	}
	for _, s := range list {
		if s.KeyType != models.KeyTypeGitHubToken {
			continue
		}
		plaintext, derr := secrets.Decrypt(s.EncryptedValue)
		if derr != nil {
			return "", fmt.Errorf("decrypt github token %q: %w", s.KeyName, derr)
		}
		return plaintext, nil
	}
	return "", nil
}
