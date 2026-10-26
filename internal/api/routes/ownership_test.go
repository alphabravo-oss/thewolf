package routes

import (
	"testing"

	"github.com/alphabravocompany/thewolf/internal/auth"
)

func TestOwnershipRequiresIdentityAndPreservesPrivateFixScope(t *testing.T) {
	for _, tc := range []struct {
		name             string
		claims           *auth.Claims
		owner            string
		general, private bool
	}{
		{"anonymous", nil, "", false, false},
		{"empty principal", &auth.Claims{}, "", false, false},
		{"empty admin principal", &auth.Claims{Role: "admin"}, "", false, false},
		{"owner", &auth.Claims{UserID: "alice"}, "alice", true, true},
		{"foreign user", &auth.Claims{UserID: "bob"}, "alice", false, false},
		{"foreign administrator", &auth.Claims{UserID: "bob", Role: "admin"}, "alice", true, false},
		{"ownerless user", &auth.Claims{UserID: "alice"}, "", false, false},
		{"ownerless administrator", &auth.Claims{UserID: "alice", Role: "admin"}, "", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := canModifyOwned(tc.claims, tc.owner); got != tc.general {
				t.Errorf("general=%v, want %v", got, tc.general)
			}
			if got := ownsFixResource(tc.owner, tc.claims); got != tc.private {
				t.Errorf("private=%v, want %v", got, tc.private)
			}
		})
	}
}
