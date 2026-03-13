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

// BrowseLocal lists directories (and identifies git repos) at a given path.
// Query params:
//   - path: directory to list (defaults to user home)
func BrowseLocal(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("path")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			response.WriteError(w, http.StatusInternalServerError, "server_error", "cannot determine home directory")
			return
		}
		dir = home
	}

	// Clean and resolve the path
	dir = filepath.Clean(dir)

	info, err := os.Stat(dir)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "path not found")
		return
	}
	if !info.IsDir() {
		dir = filepath.Dir(dir)
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

	// Include parent directory info
	parent := filepath.Dir(dir)
	if parent == dir {
		parent = "" // at root
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: map[string]interface{}{
			"current": dir,
			"parent":  parent,
			"entries": result,
		},
	})
}
