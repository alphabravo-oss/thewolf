package routes

import (
	"errors"
	"io/fs"
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
// Pulled from $WOLF_BROWSE_ROOTS (platform path-list separator) if set, otherwise
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
		if resolved, err := filepath.EvalSymlinks(abs); err == nil && resolved != abs {
			out = append(out, resolved)
		}
		out = append(out, abs)
	}
	if env := os.Getenv("WOLF_BROWSE_ROOTS"); env != "" {
		for _, p := range filepath.SplitList(env) {
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
		if relative, err := filepath.Rel(root, dir); err == nil && filepath.IsLocal(relative) {
			return true
		}
	}
	return false
}

// openBrowseDirectory checks the lexical boundary before any requested-path
// lookup, then confines every lookup/open to an administrator-configured root.
// The returned directory stays pinned if a repository path is replaced later.
func openBrowseDirectory(path string, roots []string) (*os.Root, string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, "", err
	}
	for _, allowed := range roots {
		relative, err := filepath.Rel(allowed, abs)
		if err != nil || !filepath.IsLocal(relative) {
			continue
		}
		root, err := os.OpenRoot(allowed)
		if err != nil {
			return nil, "", err
		}
		defer func() { _ = root.Close() }()
		info, err := root.Stat(relative)
		if err != nil {
			return nil, "", err
		}
		if !info.IsDir() {
			abs, relative = filepath.Dir(abs), filepath.Dir(relative)
		}
		directory, err := root.OpenRoot(relative)
		return directory, abs, err
	}
	return nil, "", os.ErrPermission
}

func writeBrowsePathError(w http.ResponseWriter, err error) {
	if os.IsNotExist(err) {
		response.WriteError(w, http.StatusNotFound, "not_found", "path not found")
		return
	}
	response.WriteError(w, http.StatusForbidden, "forbidden", "path is outside the allowed browse roots or cannot be opened")
}

// BrowseLocal lists directories (and identifies git repos) at a given path.
// Query params:
//   - path: directory to list (defaults to user home)
//
// The requested path is checked against browseAllowedRoots before lookup,
// then opened through os.Root to confine symlinks and concurrent renames.
// Paths outside the allow-list return 403,
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

	root, dir, err := openBrowseDirectory(dir, roots)
	if err != nil {
		writeBrowsePathError(w, err)
		return
	}
	defer func() { _ = root.Close() }()
	entries, err := fs.ReadDir(root.FS(), ".")
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
		if _, err := root.Stat(filepath.Join(e.Name(), ".git")); err == nil {
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

// Metadata pointers must identify a directory, unlike the picker which also
// accepts a selected file and browses its parent.
func openBrowseGitDirectory(path string, roots []string) (*os.Root, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	directory, opened, err := openBrowseDirectory(absolute, roots)
	if err == nil && opened != absolute {
		_ = directory.Close()
		return nil, errors.New("Git metadata path is not a directory")
	}
	return directory, err
}
