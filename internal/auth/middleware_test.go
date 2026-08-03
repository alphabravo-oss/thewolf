package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/auth/apikey"
)

func TestHumanSessionScopesAreDerivedFromCurrentRole(t *testing.T) {
	SetJWTSecret([]byte("middleware-role-test-secret-32-bytes"))
	originalRoleResolver := RoleResolver
	originalAuthorizationResolver := HumanAuthorizationResolver
	originalSessionResolver := resolveSession
	t.Cleanup(func() {
		RoleResolver = originalRoleResolver
		HumanAuthorizationResolver = originalAuthorizationResolver
		resolveSession = originalSessionResolver
	})
	HumanAuthorizationResolver = nil

	roles := map[string]string{"admin-id": "admin", "user-id": "user"}
	RoleResolver = func(_ context.Context, userID string) string {
		return roles[userID]
	}
	SetSessionResolver(func(_ context.Context, plaintext string) (*ResolvedSession, error) {
		if plaintext != "wfs_session-user-token" {
			return nil, nil
		}
		return &ResolvedSession{SessionID: "session-id", UserID: "user-id", Email: "user@example.test"}, nil
	})

	assertScopes := func(t *testing.T, request *http.Request, wantAdmin bool) {
		t.Helper()
		called := false
		handler := Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			called = true
			info := GetAuthInfo(r.Context())
			if info == nil {
				t.Fatal("missing auth info")
			}
			if got := info.Scopes.Has(apikey.ScopeAdmin); got != wantAdmin {
				t.Fatalf("admin scope = %v, want %v", got, wantAdmin)
			}
			if !info.Scopes.Has(apikey.ScopeReadScannerSupplyChain) {
				t.Fatal("human session must retain scanner inventory visibility")
			}
			for _, privileged := range []string{
				apikey.ScopeOperateScannerSupplyChain,
				apikey.ScopeApproveScannerReleases,
				apikey.ScopeManageScannerRegistries,
				apikey.ScopeAdminScannerSupplyChain,
			} {
				if got := info.Scopes.Has(privileged); got != wantAdmin {
					t.Fatalf("scope %q = %v, want %v", privileged, got, wantAdmin)
				}
			}
		}))
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK || !called {
			t.Fatalf("status = %d, called = %v, body = %s", recorder.Code, called, recorder.Body.String())
		}
	}

	adminPair, err := GenerateToken("admin-id", "admin@example.test")
	if err != nil {
		t.Fatal(err)
	}
	adminRequest := httptest.NewRequest(http.MethodGet, "/api/v1/scanner-supply-chain/overview", nil)
	adminRequest.Header.Set("Authorization", "Bearer "+adminPair.AccessToken)
	assertScopes(t, adminRequest, true)

	userPair, err := GenerateToken("user-id", "user@example.test")
	if err != nil {
		t.Fatal(err)
	}
	userRequest := httptest.NewRequest(http.MethodGet, "/api/v1/scanner-supply-chain/overview", nil)
	userRequest.Header.Set("Authorization", "Bearer "+userPair.AccessToken)
	assertScopes(t, userRequest, false)

	cookieRequest := httptest.NewRequest(http.MethodGet, "/api/v1/scanner-supply-chain/overview", nil)
	cookieRequest.AddCookie(&http.Cookie{Name: "wolf_token", Value: "wfs_session-user-token"})
	assertScopes(t, cookieRequest, false)
}

func TestAPITokenScopesRemainTokenBound(t *testing.T) {
	originalTokenResolver := resolveAPIToken
	originalRoleResolver := RoleResolver
	originalAuthorizationResolver := HumanAuthorizationResolver
	t.Cleanup(func() {
		resolveAPIToken = originalTokenResolver
		RoleResolver = originalRoleResolver
		HumanAuthorizationResolver = originalAuthorizationResolver
	})
	HumanAuthorizationResolver = nil
	SetAPITokenResolver(func(_ context.Context, _ string) (*ResolvedToken, error) {
		return &ResolvedToken{
			TokenID: "token-id", UserID: "admin-id", Email: "admin@example.test",
			Scopes: []string{apikey.ScopeReadScannerSupplyChain},
		}, nil
	})
	RoleResolver = func(_ context.Context, _ string) string { return "admin" }

	request := httptest.NewRequest(http.MethodGet, "/api/v1/scanner-supply-chain/overview", nil)
	request.Header.Set("Authorization", "Bearer wolf_test_token")
	recorder := httptest.NewRecorder()
	Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		info := GetAuthInfo(r.Context())
		if info.Scopes.Has(apikey.ScopeAdmin) || info.Scopes.Has(apikey.ScopeOperateScannerSupplyChain) {
			t.Fatal("API token was escalated by the owner's human role")
		}
		if !info.Scopes.Has(apikey.ScopeReadScannerSupplyChain) {
			t.Fatal("API token lost its explicit scope")
		}
	})).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestHumanScannerPersonasAndRevocationApplyWithoutRelogin(t *testing.T) {
	SetJWTSecret([]byte("middleware-persona-test-secret-32b"))
	originalAuthorizationResolver := HumanAuthorizationResolver
	t.Cleanup(func() { HumanAuthorizationResolver = originalAuthorizationResolver })

	currentRole := "user"
	currentPersonas := []string{apikey.ScannerPersonaViewer}
	HumanAuthorizationResolver = func(_ context.Context, userID string) (HumanAuthorization, error) {
		if userID != "user-id" {
			t.Fatalf("resolver user ID = %q", userID)
		}
		return HumanAuthorization{Role: currentRole, ScannerPersonas: append([]string(nil), currentPersonas...)}, nil
	}

	pair, err := GenerateToken("user-id", "user@example.test")
	if err != nil {
		t.Fatal(err)
	}
	request := func() *AuthInfo {
		t.Helper()
		var got *AuthInfo
		req := httptest.NewRequest(http.MethodGet, "/api/v1/scanner-supply-chain/overview", nil)
		req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
		rec := httptest.NewRecorder()
		Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			got = GetAuthInfo(r.Context())
		})).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || got == nil {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
		return got
	}

	tests := []struct {
		persona string
		scope   string
	}{
		{apikey.ScannerPersonaViewer, apikey.ScopeReadScannerSupplyChain},
		{apikey.ScannerPersonaOperator, apikey.ScopeOperateScannerSupplyChain},
		{apikey.ScannerPersonaApprover, apikey.ScopeApproveScannerReleases},
		{apikey.ScannerPersonaRegistryAdministrator, apikey.ScopeManageScannerRegistries},
		{apikey.ScannerPersonaSupplyChainAdministrator, apikey.ScopeAdminScannerSupplyChain},
		{apikey.ScannerPersonaAuditor, apikey.ScopeReadScannerSupplyChain},
	}
	for _, tt := range tests {
		currentPersonas = []string{tt.persona}
		info := request()
		if !info.Scopes.Has(tt.scope) {
			t.Errorf("persona %q missing scope %q from %v", tt.persona, tt.scope, info.Scopes)
		}
		if len(info.ScannerPersonas) != 1 || info.ScannerPersonas[0] != tt.persona {
			t.Errorf("persona metadata = %v, want %q", info.ScannerPersonas, tt.persona)
		}
	}

	// Reuse the same signed token after changing persisted authorization. The
	// next request must lose the elevated scope without a login or token refresh.
	currentPersonas = []string{apikey.ScannerPersonaSupplyChainAdministrator}
	if !request().Scopes.Has(apikey.ScopeAdminScannerSupplyChain) {
		t.Fatal("precondition: supply-chain admin scope missing")
	}
	currentPersonas = []string{apikey.ScannerPersonaViewer}
	revoked := request()
	for _, scope := range []string{
		apikey.ScopeOperateScannerSupplyChain,
		apikey.ScopeApproveScannerReleases,
		apikey.ScopeManageScannerRegistries,
		apikey.ScopeAdminScannerSupplyChain,
	} {
		if revoked.Scopes.Has(scope) {
			t.Errorf("revoked session retained %q", scope)
		}
	}

	currentRole = "admin"
	if !request().Scopes.Has(apikey.ScopeAdmin) {
		t.Fatal("system administrators must retain implicit full access")
	}
}
