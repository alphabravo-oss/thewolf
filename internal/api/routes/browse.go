package routes

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/api/response"
)

type dirEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
	IsGit bool   `json:"is_git"`
}

// browseAllowedRoots returns the absolute, symlink-evaluated set of
// directories that authenticated users may browse to pick repos from.
// Pulled from $WOLF_BROWSE_ROOTS (colon-separated) if set, otherwise
// falls back to the user's home directory. Returning the empty list
// short-circuits BrowseLocal into a 500.
func browseAllowedRoots() []string {
	var out []string
	add := func(p string) {
		if p == "" {
			return
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return
		}
		if resolved, err := filepath.EvalSymlinks(abs); err == nil {
			abs = resolved
		}
		out = append(out, abs)
	}
	if env := os.Getenv("WOLF_BROWSE_ROOTS"); env != "" {
		for _, p := range strings.Split(env, ":") {
			add(strings.TrimSpace(p))
		}
		return out
	}
	if home, err := os.UserHomeDir(); err == nil {
		add(home)
	}
	return out
}

// isUnderRoot reports whether `dir` (already absolute + symlink-resolved)
// lies inside one of the allow-listed roots. Comparison uses path
// segments — a prefix string match like /home/alice would otherwise
// admit /home/alice-evil/.
func isUnderRoot(dir string, roots []string) bool {
	for _, root := range roots {
		if dir == root {
			return true
		}
		// Append the separator so /home/alice doesn't match /home/alice-evil.
		if strings.HasPrefix(dir, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// BrowseLocal lists directories (and identifies git repos) at a given path.
// Query params:
//   - path: directory to list (defaults to user home)
//
// The path is resolved to an absolute, symlink-evaluated form and then
// checked against browseAllowedRoots (default: user home; overridable
// via WOLF_BROWSE_ROOTS). Paths outside the allow-list return 403,
// preventing an authenticated user from enumerating arbitrary
// directories on the server (e.g. /etc, /var, ../../../).
func BrowseLocal(w http.ResponseWriter, r *http.Request) {
	roots := browseAllowedRoots()
	if len(roots) == 0 {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "no allow-listed browse roots configured")
		return
	}

	dir := r.URL.Query().Get("path")
	if dir == "" {
		dir = roots[0]
	}

	// Resolve to an absolute, symlink-evaluated path before any check.
	// filepath.Clean alone does NOT prevent traversal:
	// "/home/alice/../../etc" cleans to "/etc", which still resolves.
	abs, err := filepath.Abs(dir)
	if err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid path")
		return
	}
	if resolved, rerr := filepath.EvalSymlinks(abs); rerr == nil {
		abs = resolved
	}
	dir = abs

	info, err := os.Stat(dir)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "path not found")
		return
	}
	if !info.IsDir() {
		dir = filepath.Dir(dir)
	}

	if !isUnderRoot(dir, roots) {
		response.WriteError(w, http.StatusForbidden, "forbidden", "path is outside the allowed browse roots")
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		response.WriteError(w, http.StatusForbidden, "forbidden", "cannot read directory")
		return
	}

	var result []dirEntry
	for _, e := range entries {
		// Skip hidden files/dirs
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if !e.IsDir() {
			continue
		}

		fullPath := filepath.Join(dir, e.Name())
		isGit := false
		if _, err := os.Stat(filepath.Join(fullPath, ".git")); err == nil {
			isGit = true
		}

		result = append(result, dirEntry{
			Name:  e.Name(),
			Path:  fullPath,
			IsDir: true,
			IsGit: isGit,
		})
	}

	sort.Slice(result, func(i, j int) bool {
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})

	// Parent dir — only set if the parent is still inside an allow-listed
	// root. Otherwise the UI shouldn't render an "up" link that 403s.
	parent := filepath.Dir(dir)
	if parent == dir || !isUnderRoot(parent, roots) {
		parent = ""
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: map[string]interface{}{
			"current": dir,
			"parent":  parent,
			"entries": result,
		},
	})
}
