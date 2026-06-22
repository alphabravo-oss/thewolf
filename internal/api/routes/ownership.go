package routes

import (
	"net/http"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
)

// canModifyOwned reports whether the caller may modify/delete a resource owned
// by ownerUserID. Admins can modify anything; a regular user can only modify
// what they created. An empty ownerUserID (legacy/system-created rows) is
// treated as modifiable so pre-RBAC data isn't stranded.
func canModifyOwned(claims *auth.Claims, ownerUserID string) bool {
	if claims == nil {
		return false
	}
	if claims.IsAdmin() {
		return true
	}
	return ownerUserID == "" || ownerUserID == claims.UserID
}

func loadRepoForCaller(w http.ResponseWriter, r *http.Request, store db.Store, repoID string, claims *auth.Claims) (*models.Repo, bool) {
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return nil, false
	}
	repo, err := store.GetRepoByID(r.Context(), repoID)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "repo not found")
		return nil, false
	}
	if !canModifyOwned(claims, repo.UserID) {
		response.WriteError(w, http.StatusForbidden, "forbidden", "repo does not belong to current user")
		return nil, false
	}
	return repo, true
}

func loadScanForCaller(w http.ResponseWriter, r *http.Request, store db.Store, scanID string, claims *auth.Claims) (*models.Scan, bool) {
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return nil, false
	}
	scan, err := store.GetScanByID(r.Context(), scanID)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "scan not found")
		return nil, false
	}
	if !ensureScanOwner(w, scan, claims) {
		return nil, false
	}
	return scan, true
}
