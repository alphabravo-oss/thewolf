package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/thewolf/pkg/authprovider"
	"github.com/alphabravocompany/thewolf/pkg/entitlement"
)

type ssoStub struct{}

func (ssoStub) Name() string { return "oidc" }
func (ssoStub) Authenticate(context.Context, string, string) (string, error) {
	return "", nil
}
func (ssoStub) AuthorizationURL(state, nonce, redirectURI string) (string, error) {
	return "https://idp.example/authorize?state=" + state, nil
}
func (ssoStub) Redeem(context.Context, string, string) (string, error) {
	return "sso@example.com", nil
}

type ssoAllow struct{}

func (ssoAllow) Allows(string) bool { return true }

func TestStartSSORedirectsWhenProviderRegistered(t *testing.T) {
	entitlement.SetActive(ssoAllow{})
	t.Cleanup(func() { entitlement.SetActive(nil) })
	reg := authprovider.New()
	reg.Register(ssoStub{})
	r := chi.NewRouter()
	r.Get("/api/v1/auth/sso/{name}/start", func(w http.ResponseWriter, req *http.Request) {
		startSSO(w, req, reg)
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/sso/oidc/start", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("start: %d %s", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); !strings.Contains(loc, "idp.example") {
		t.Fatalf("location = %q", loc)
	}
}

func TestStartSSOUnknownProvider404(t *testing.T) {
	reg := authprovider.New()
	r := chi.NewRouter()
	r.Get("/api/v1/auth/sso/{name}/start", func(w http.ResponseWriter, req *http.Request) {
		startSSO(w, req, reg)
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/sso/oidc/start", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("start missing: %d", w.Code)
	}
}

func TestSSOCallbackAcceptsSAMLPost(t *testing.T) {
	entitlement.SetActive(ssoAllow{})
	t.Cleanup(func() { entitlement.SetActive(nil) })
	reg := authprovider.New()
	reg.Register(ssoStub{})
	r := chi.NewRouter()
	r.Get("/api/v1/auth/sso/{name}/start", func(w http.ResponseWriter, req *http.Request) {
		startSSO(w, req, reg)
	})
	r.Post("/api/v1/auth/sso/{name}/callback", func(w http.ResponseWriter, req *http.Request) {
		ssoCallback(w, req, reg)
	})
	start := httptest.NewRequest(http.MethodGet, "/api/v1/auth/sso/oidc/start", nil)
	sw := httptest.NewRecorder()
	r.ServeHTTP(sw, start)
	loc := sw.Header().Get("Location")
	state := loc[strings.LastIndex(loc, "state=")+6:]
	if i := strings.IndexByte(state, '&'); i >= 0 {
		state = state[:i]
	}
	form := "SAMLResponse=assertion&RelayState=" + state
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/sso/oidc/callback", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	// No store is wired, so provisioning fails after a valid Redeem — not 401 state.
	if w.Code == http.StatusUnauthorized && strings.Contains(w.Body.String(), "invalid sso state") {
		t.Fatalf("SAML POST must accept RelayState without cookie: %d %s", w.Code, w.Body.String())
	}
}

func TestSCIMDisabledOnCommunity(t *testing.T) {
	entitlement.SetActive(entitlement.Community{})
	t.Cleanup(func() { entitlement.SetActive(nil) })
	req := httptest.NewRequest(http.MethodGet, "/api/v1/scim/v2/Users", nil)
	w := httptest.NewRecorder()
	SCIMUnavailable(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("scim community: %d %s", w.Code, w.Body.String())
	}
}
