package routes

import "github.com/alphabravocompany/thewolf/internal/auth"

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
