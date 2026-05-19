package routes

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	gitpkg "github.com/alphabravocompany/thewolf/internal/fix/git"
)

// GitInfo handles GET /api/git-info?path=... — given an arbitrary directory,
// reports whether it's a git working tree and (if so) lists its branches and
// current HEAD branch. Used by the "Add local repo" flow so the form can show
// a real branch dropdown instead of asking the user to type one.
//
// Security: the path is run through the same allow-list check as BrowseLocal
// before any git command is invoked, so an authenticated user cannot point
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

	abs, err := filepath.Abs(raw)
	if err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid path")
		return
	}
	if resolved, rerr := filepath.EvalSymlinks(abs); rerr == nil {
		abs = resolved
	}

	info, err := os.Stat(abs)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "path not found")
		return
	}
	if !info.IsDir() {
		abs = filepath.Dir(abs)
	}

	if !isUnderRoot(abs, roots) {
		response.WriteError(w, http.StatusForbidden, "forbidden", "path is outside the allowed browse roots")
		return
	}

	if _, err := os.Stat(filepath.Join(abs, ".git")); err != nil {
		response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
			Data: map[string]interface{}{
				"path":   abs,
				"is_git": false,
			},
		})
		return
	}

	// .git exists, so the user clearly picked a git repo — surface any
	// failure (missing `git` binary in the runtime, corrupt repo, etc.)
	// rather than silently flipping to is_git=false, which would hide
	// real misconfigurations from the operator.
	branches, berr := gitpkg.ListBranches(abs)
	if berr != nil {
		response.WriteError(w, http.StatusInternalServerError, "git_error", "failed to list branches: "+berr.Error())
		return
	}
	sort.Strings(branches)
	current, _ := gitpkg.CurrentBranch(abs)

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: map[string]interface{}{
			"path":           abs,
			"is_git":         true,
			"branches":       branches,
			"current_branch": current,
		},
	})
}
