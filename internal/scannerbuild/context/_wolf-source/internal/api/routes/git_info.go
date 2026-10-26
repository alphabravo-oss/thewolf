package routes

import (
	"errors"
	"net/http"
	"os"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	gitpkg "github.com/alphabravocompany/thewolf/internal/fix/git"
)

// GitInfo handles GET /api/git-info?path=... — given an arbitrary directory,
// reports whether it's a git working tree and (if so) lists its branches and
// current HEAD branch. Used by the "Add local repo" flow so the form can show
// a real branch dropdown instead of asking the user to type one.
//
// Security: the path is run through the same allow-list check as BrowseLocal
// before reference metadata is read, so an authenticated user cannot point
// this at arbitrary paths (e.g. /var/lib/...) and probe whether they contain
// repos.
func GitInfo(w http.ResponseWriter, r *http.Request) {
	roots := browseAllowedRoots()
	if len(roots) == 0 {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "no allow-listed browse roots configured")
		return
	}

	raw := r.URL.Query().Get("path")
	if raw == "" {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "path is required")
		return
	}

	root, abs, err := openBrowseDirectory(raw, roots)
	if err != nil {
		writeBrowsePathError(w, err)
		return
	}
	defer func() { _ = root.Close() }()

	if _, err := root.Lstat(".git"); errors.Is(err, os.ErrNotExist) {
		response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
			Data: map[string]interface{}{
				"path":   abs,
				"is_git": false,
			},
		})
		return
	}

	// Worktree/submodule pointers and commondir must stay within the same
	// administrator-approved roots; no pathname is handed to a subprocess.
	branches, current, err := gitpkg.BranchesRoot(r.Context(), root, func(path string) (*os.Root, error) {
		return openBrowseGitDirectory(path, roots)
	})
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "git_error", "failed to read branch metadata: "+err.Error())
		return
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: map[string]interface{}{
			"path":           abs,
			"is_git":         true,
			"branches":       branches,
			"current_branch": current,
		},
	})
}
