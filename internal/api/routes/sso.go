package routes

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/pkg/authprovider"
	"github.com/alphabravocompany/thewolf/pkg/control"
	"github.com/alphabravocompany/thewolf/pkg/entitlement"
)

type ssoPending struct {
	nonce    string
	provider string
	exp      time.Time
}

var (
	ssoMu    sync.Mutex
	ssoState = map[string]ssoPending{}
)

func StartSSO(w http.ResponseWriter, r *http.Request) {
	startSSO(w, r, authprovider.Default)
}

func startSSO(w http.ResponseWriter, r *http.Request, reg *authprovider.Registry) {
	name := chi.URLParam(r, "name")
	if name == "" || name == authprovider.Local || !entitlement.Active().Allows(entitlement.Identity) {
		response.WriteError(w, http.StatusNotFound, "not_found", "sso provider not found")
		return
	}
	p := reg.Lookup(name)
	redir, ok := p.(authprovider.Redirector)
	if !ok || redir == nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "sso provider not found")
		return
	}
	state, nonce, err := randomSSOPair()
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to start sso")
		return
	}
	ssoMu.Lock()
	ssoState[state] = ssoPending{nonce: nonce, provider: name, exp: time.Now().Add(10 * time.Minute)}
	ssoMu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name:     "wolf_sso",
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		Secure:   cookieSecure(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600,
	})
	redirectURI := ssoRedirectURI(r, name)
	loc, err := redir.AuthorizationURL(state, nonce, redirectURI)
	if err != nil || loc == "" {
		response.WriteError(w, http.StatusBadGateway, "sso_unconfigured", "sso provider is not configured")
		return
	}
	http.Redirect(w, r, loc, http.StatusFound)
}

func SSOCallback(w http.ResponseWriter, r *http.Request) {
	ssoCallback(w, r, authprovider.Default)
}

func ssoCallback(w http.ResponseWriter, r *http.Request, reg *authprovider.Registry) {
	name := chi.URLParam(r, "name")
	_ = r.ParseForm()
	code := firstForm(r, "code", "SAMLResponse")
	state := firstForm(r, "state", "RelayState")
	if name == "" || code == "" || state == "" {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "code and state are required")
		return
	}
	c, _ := r.Cookie("wolf_sso")
	cookieOK := c != nil && c.Value == state
	ssoMu.Lock()
	pending, ok := ssoState[state]
	delete(ssoState, state)
	ssoMu.Unlock()
	if !ok || time.Now().After(pending.exp) || pending.provider != name {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid sso state")
		return
	}
	// SAML HTTP-POST ACS is a cross-site POST; SameSite=Lax omits the cookie.
	// RelayState is the CSRF token in that case.
	if !cookieOK && (r.Method != http.MethodPost || r.Form.Get("SAMLResponse") == "") {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid sso state")
		return
	}
	if !entitlement.Active().Allows(entitlement.Identity) {
		response.WriteError(w, http.StatusNotFound, "not_found", "sso provider not found")
		return
	}
	p := reg.Lookup(name)
	redir, ok := p.(authprovider.Redirector)
	if !ok || redir == nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "sso provider not found")
		return
	}
	email, groups, err := redeemSSO(r.Context(), redir, code, ssoRedirectURI(r, name))
	if err != nil || strings.TrimSpace(email) == "" {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "sso login failed")
		return
	}
	email = strings.ToLower(strings.TrimSpace(email))
	user, err := ensureSSOUser(r, email)
	if err != nil {
		var le *communityLimitError
		if errors.As(err, &le) {
			response.WriteError(w, http.StatusConflict, le.code, le.msg)
			return
		}
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to provision user")
		return
	}
	applySSOGroups(r.Context(), user, groups)
	if err := issueSessionCookie(w, r, user.ID); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to create session")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: "wolf_sso", Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
	})
	RecordAuthEvent(r, user.ID, "auth.sso.login", "info", http.StatusOK)
	http.Redirect(w, r, "/", http.StatusFound)
}

func firstForm(r *http.Request, keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(r.Form.Get(k)); v != "" {
			return v
		}
	}
	return ""
}

func ssoRedirectURI(r *http.Request, name string) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host + "/api/v1/auth/sso/" + name + "/callback"
}

func randomSSOPair() (state, nonce string, err error) {
	a := make([]byte, 16)
	b := make([]byte, 16)
	if _, err = rand.Read(a); err != nil {
		return "", "", err
	}
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	return hex.EncodeToString(a), hex.EncodeToString(b), nil
}

func ensureSSOUser(r *http.Request, email string) (*models.User, error) {
	h := DefaultHandler
	if h == nil || h.Store == nil {
		return nil, http.ErrServerClosed
	}
	if u, err := h.Store.GetUserByEmail(r.Context(), email); err == nil && u != nil {
		return u, nil
	}
	if err := checkCommunityLimit(r.Context(), h.Store, limitUsers); err != nil {
		return nil, err
	}
	secret := make([]byte, 32)
	_, _ = rand.Read(secret)
	hash, err := auth.HashPassword(hex.EncodeToString(secret))
	if err != nil {
		return nil, err
	}
	u := &models.User{
		ID:           uuid.NewString(),
		Email:        email,
		PasswordHash: hash,
		Role:         models.RoleUser,
	}
	if err := h.Store.CreateUser(r.Context(), u); err != nil {
		return nil, err
	}
	return u, nil
}

func redeemSSO(ctx context.Context, redir authprovider.Redirector, code, redirectURI string) (string, []string, error) {
	if g, ok := redir.(authprovider.GroupedRedirector); ok {
		id, err := g.RedeemIdentity(ctx, code, redirectURI)
		return id.Email, id.Groups, err
	}
	email, err := redir.Redeem(ctx, code, redirectURI)
	return email, nil, err
}

func applySSOGroups(ctx context.Context, user *models.User, groups []string) {
	if user == nil || len(groups) == 0 {
		return
	}
	api := control.RecordsAPI()
	if api == nil {
		return
	}
	recs, err := api.List(ctx, "group-mappings")
	if err != nil || len(recs) == 0 {
		return
	}
	role, ok := mappedWolfRole(groups, recs)
	if !ok || role == string(user.Role) {
		return
	}
	user.Role = role
	if DefaultHandler != nil && DefaultHandler.Store != nil {
		_ = DefaultHandler.Store.UpdateUser(ctx, user)
	}
}

func mappedWolfRole(groups []string, recs []control.Record) (string, bool) {
	if len(groups) == 0 {
		return "", false
	}
	byName := map[string]string{}
	for _, rec := range recs {
		r := mappingRole(rec.Body)
		if r != "admin" && r != "user" {
			continue
		}
		byName[rec.Name] = r
	}
	found, role := false, "user"
	for _, g := range groups {
		r, ok := byName[g]
		if !ok {
			continue
		}
		found = true
		if r == "admin" {
			return "admin", true
		}
		role = r
	}
	return role, found
}

func mappingRole(body string) string {
	body = strings.TrimSpace(body)
	var parsed struct {
		Role string `json:"role"`
	}
	if json.Unmarshal([]byte(body), &parsed) == nil && parsed.Role != "" {
		return strings.ToLower(strings.TrimSpace(parsed.Role))
	}
	return strings.ToLower(body)
}
