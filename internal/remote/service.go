package remote

import (
	"context"
	"encoding/base64"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/secrets"
	"github.com/alphabravocompany/thewolf/internal/sshclient"
)

type DirEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
	IsGit bool   `json:"is_git"`
}

type BrowseResult struct {
	Current string     `json:"current"`
	Parent  string     `json:"parent"`
	Entries []DirEntry `json:"entries"`
}

type DiscoveredRepo struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Branch    string `json:"branch,omitempty"`
	CommitSHA string `json:"commit_sha,omitempty"`
}

type GitInfo struct {
	Path          string   `json:"path"`
	IsGit         bool     `json:"is_git"`
	Branches      []string `json:"branches,omitempty"`
	CurrentBranch string   `json:"current_branch,omitempty"`
	CommitSHA     string   `json:"commit_sha,omitempty"`
	DirtyState    string   `json:"dirty_state,omitempty"`
}

type Service struct {
	Store  db.Store
	Runner sshclient.Runner
}

func (s Service) runner() sshclient.Runner {
	if s.Runner != nil {
		return s.Runner
	}
	return sshclient.Client{}
}

func (s Service) ConfigForNode(ctx context.Context, node *models.RemoteNode) (sshclient.Config, error) {
	if node == nil {
		return sshclient.Config{}, fmt.Errorf("remote node is required")
	}
	if !node.Enabled {
		return sshclient.Config{}, fmt.Errorf("remote node is disabled")
	}
	if node.CredentialSecretID == nil || *node.CredentialSecretID == "" {
		return sshclient.Config{}, fmt.Errorf("remote node credential secret is required")
	}
	secret, err := s.Store.GetSecretByID(ctx, *node.CredentialSecretID)
	if err != nil {
		return sshclient.Config{}, fmt.Errorf("load SSH credential secret: %w", err)
	}
	if secret.UserID != node.UserID {
		return sshclient.Config{}, fmt.Errorf("SSH credential secret does not belong to remote node owner")
	}
	credential, err := secrets.Decrypt(secret.EncryptedValue)
	if err != nil {
		return sshclient.Config{}, fmt.Errorf("decrypt SSH credential secret: %w", err)
	}
	cfg := sshclient.Config{
		Host:       node.Host,
		Port:       node.Port,
		Username:   node.Username,
		KnownHosts: node.KnownHosts,
		Timeout:    30 * time.Second,
	}
	switch node.AuthType {
	case "", "private_key":
		if secret.KeyType != models.KeyTypeSSHPrivate {
			return sshclient.Config{}, fmt.Errorf("SSH private-key auth requires an ssh_private_key secret")
		}
		cfg.PrivateKey = credential
	case "password":
		if secret.KeyType != models.KeyTypeSSHPassword {
			return sshclient.Config{}, fmt.Errorf("SSH password auth requires an ssh_password secret")
		}
		cfg.Password = credential
	default:
		return sshclient.Config{}, fmt.Errorf("unsupported SSH auth_type %q", node.AuthType)
	}
	return cfg, nil
}

func (s Service) Check(ctx context.Context, node *models.RemoteNode) error {
	cfg, err := s.ConfigForNode(ctx, node)
	if err != nil {
		return err
	}
	cmd := "printf wolf-ok && command -v git >/dev/null && command -v tar >/dev/null && test -d " + sshclient.ShellQuote(defaultPath(node.BasePath))
	_, err = s.runner().Run(ctx, cfg, cmd)
	return err
}

func (s Service) Browse(ctx context.Context, node *models.RemoteNode, dir string) (BrowseResult, error) {
	cfg, err := s.ConfigForNode(ctx, node)
	if err != nil {
		return BrowseResult{}, err
	}
	if strings.TrimSpace(dir) == "" {
		dir = defaultPath(node.BasePath)
	}
	cmd := `dir=` + sshclient.ShellQuote(dir) + `;
base=` + sshclient.ShellQuote(node.BasePath) + `;
if [ ! -d "$dir" ]; then exit 44; fi
cd "$dir" || exit 44
current=$(pwd -P)
if [ -n "$base" ]; then
  base_real=$(cd "$base" 2>/dev/null && pwd -P) || exit 45
  if [ "$current" != "$base_real" ]; then
    case "$current/" in "$base_real"/*) ;; *) exit 45;; esac
  fi
fi
printf 'CURRENT\t%s\n' "$current"
parent=$(dirname "$current")
printf 'PARENT\t%s\n' "$parent"
for p in *; do
  [ -d "$p" ] || continue
  case "$p" in .*) continue;; esac
  full="$(pwd -P)/$p"
  git=0
  [ -d "$full/.git" ] && git=1
  printf 'ENTRY\t%s\t%s\t%s\n' "$p" "$full" "$git"
done`
	res, err := s.runner().Run(ctx, cfg, cmd)
	if err != nil {
		return BrowseResult{}, err
	}
	return parseBrowse(res.Stdout), nil
}

func (s Service) GitInfo(ctx context.Context, node *models.RemoteNode, repoPath string) (GitInfo, error) {
	cfg, err := s.ConfigForNode(ctx, node)
	if err != nil {
		return GitInfo{}, err
	}
	cmd := `repo=` + sshclient.ShellQuote(repoPath) + `;
base=` + sshclient.ShellQuote(node.BasePath) + `;
if [ ! -d "$repo" ]; then exit 44; fi
cd "$repo" || exit 44
current=$(pwd -P)
if [ -n "$base" ]; then
  base_real=$(cd "$base" 2>/dev/null && pwd -P) || exit 45
  if [ "$current" != "$base_real" ]; then
    case "$current/" in "$base_real"/*) ;; *) exit 45;; esac
  fi
fi
printf 'PATH\t%s\n' "$current"
if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  printf 'IS_GIT\tfalse\n'
  exit 0
fi
printf 'IS_GIT\ttrue\n'
printf 'CURRENT\t%s\n' "$(git rev-parse --abbrev-ref HEAD 2>/dev/null || true)"
printf 'COMMIT\t%s\n' "$(git rev-parse HEAD 2>/dev/null || true)"
dirty=clean
if [ -n "$(git status --porcelain 2>/dev/null)" ]; then dirty=dirty; fi
printf 'DIRTY\t%s\n' "$dirty"
git branch --format='BRANCH	%(refname:short)' 2>/dev/null || true
git branch -r --format='BRANCH	%(refname:short)' 2>/dev/null | sed 's#^BRANCH	[^/]*/#BRANCH	#' || true`
	res, err := s.runner().Run(ctx, cfg, cmd)
	if err != nil {
		return GitInfo{}, err
	}
	return parseGitInfo(res.Stdout), nil
}

// DiscoverRepos walks the remote node looking for .git directories under
// basePath (up to maxdepth 3), and for each discovered repository returns its
// name, parent path, current branch, and commit SHA.
func (s Service) DiscoverRepos(ctx context.Context, node *models.RemoteNode, basePath string) ([]DiscoveredRepo, error) {
	cfg, err := s.ConfigForNode(ctx, node)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(basePath) == "" {
		basePath = defaultPath(node.BasePath)
	}
	cmd := `base=` + sshclient.ShellQuote(basePath) + `;
root=` + sshclient.ShellQuote(node.BasePath) + `;
if [ ! -d "$base" ]; then exit 44; fi
cd "$base" || exit 44
base_real=$(pwd -P)
if [ -n "$root" ]; then
  root_real=$(cd "$root" 2>/dev/null && pwd -P) || exit 45
  if [ "$base_real" != "$root_real" ]; then
    case "$base_real/" in "$root_real"/*) ;; *) exit 45;; esac
  fi
fi
find "$base_real" -maxdepth 3 -name .git -type d 2>/dev/null | while read -r gitdir; do
  parent=$(dirname "$gitdir")
  name=$(basename "$parent")
  branch=$(git -C "$parent" rev-parse --abbrev-ref HEAD 2>/dev/null || true)
  commit=$(git -C "$parent" rev-parse HEAD 2>/dev/null || true)
  printf 'REPO\t%s\t%s\t%s\t%s\n' "$name" "$parent" "$branch" "$commit"
done`
	res, err := s.runner().Run(ctx, cfg, cmd)
	if err != nil {
		return nil, err
	}
	return parseDiscoverRepos(res.Stdout), nil
}

func (s Service) Archive(ctx context.Context, node *models.RemoteNode, repoPath, branch string) ([]byte, GitInfo, error) {
	info, err := s.GitInfo(ctx, node, repoPath)
	if err != nil {
		return nil, GitInfo{}, err
	}
	if !info.IsGit {
		return nil, info, fmt.Errorf("remote path is not a git repository")
	}
	ref := branch
	if ref == "" {
		ref = "HEAD"
	}
	cfg, err := s.ConfigForNode(ctx, node)
	if err != nil {
		return nil, GitInfo{}, err
	}
	cmd := `cd ` + sshclient.ShellQuote(repoPath) + ` && git archive --format=tar ` + sshclient.ShellQuote(ref) + ` | base64`
	res, err := s.runner().Run(ctx, cfg, cmd)
	if err != nil {
		return nil, info, err
	}
	data, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(res.Stdout), ""))
	if err != nil {
		return nil, info, fmt.Errorf("decode remote archive: %w", err)
	}
	return data, info, nil
}

func defaultPath(p string) string {
	if strings.TrimSpace(p) != "" {
		return p
	}
	return "."
}

func parseBrowse(out string) BrowseResult {
	var r BrowseResult
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Split(line, "\t")
		if len(parts) == 0 {
			continue
		}
		switch parts[0] {
		case "CURRENT":
			if len(parts) > 1 {
				r.Current = parts[1]
			}
		case "PARENT":
			if len(parts) > 1 {
				r.Parent = parts[1]
			}
		case "ENTRY":
			if len(parts) >= 4 {
				r.Entries = append(r.Entries, DirEntry{Name: parts[1], Path: parts[2], IsDir: true, IsGit: parts[3] == "1"})
			}
		}
	}
	sort.Slice(r.Entries, func(i, j int) bool {
		return strings.ToLower(r.Entries[i].Name) < strings.ToLower(r.Entries[j].Name)
	})
	if r.Current != "" && path.Clean(r.Parent) == path.Clean(r.Current) {
		r.Parent = ""
	}
	return r
}

func parseDiscoverRepos(out string) []DiscoveredRepo {
	var repos []DiscoveredRepo
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Split(line, "\t")
		if len(parts) == 0 || parts[0] != "REPO" {
			continue
		}
		// Fields: REPO, name, path, branch, commit
		row := DiscoveredRepo{}
		if len(parts) > 1 {
			row.Name = parts[1]
		}
		if len(parts) > 2 {
			row.Path = parts[2]
		}
		if len(parts) > 3 {
			row.Branch = parts[3]
		}
		if len(parts) > 4 {
			row.CommitSHA = parts[4]
		}
		if row.Path == "" {
			continue
		}
		repos = append(repos, row)
	}
	sort.Slice(repos, func(i, j int) bool {
		return strings.ToLower(repos[i].Path) < strings.ToLower(repos[j].Path)
	})
	return repos
}

func parseGitInfo(out string) GitInfo {
	info := GitInfo{DirtyState: "unknown"}
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		switch parts[0] {
		case "PATH":
			info.Path = parts[1]
		case "IS_GIT":
			info.IsGit = parts[1] == "true"
		case "CURRENT":
			info.CurrentBranch = parts[1]
		case "COMMIT":
			info.CommitSHA = parts[1]
		case "DIRTY":
			info.DirtyState = parts[1]
		case "BRANCH":
			branch := strings.TrimSpace(parts[1])
			if branch == "" || strings.HasSuffix(branch, "/HEAD") || seen[branch] {
				continue
			}
			seen[branch] = true
			info.Branches = append(info.Branches, branch)
		}
	}
	sort.Strings(info.Branches)
	return info
}
