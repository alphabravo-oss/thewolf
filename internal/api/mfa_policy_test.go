package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/pquerna/otp/totp"

	"github.com/alphabravocompany/thewolf/internal/api/routes"
	"github.com/alphabravocompany/thewolf/internal/auth/apikey"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
)

type mfaPolicyFailureStore struct{ db.Store }

func (s mfaPolicyFailureStore) GetSetting(ctx context.Context, key string) (string, error) {
	if key == "mfa_required" {
		return "", errors.New("private MFA policy diagnostic")
	}
	return s.Store.GetSetting(ctx, key)
}

func TestMFAPolicyFailureDeniesProtectedAccess(t *testing.T) {
	srv, store, jwt := newTestServer(t)
	if err := store.SetSetting(t.Context(), "mfa_required", "true"); err != nil {
		t.Fatal(err)
	}
	handler := routes.DefaultHandler
	handler.Store = mfaPolicyFailureStore{Store: store}
	defer func() { handler.Store = store }()
	for _, path := range []string{"/api/v1/repos", "/api/v1/auth/mfa/status"} {
		w := request(srv, http.MethodGet, path, jwt, nil)
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("%s policy failure: %d", path, w.Code)
		}
		if strings.Contains(w.Body.String(), "private MFA policy") {
			t.Fatal("policy error exposed storage diagnostic")
		}
	}
}

func TestMFAPolicyFailureDoesNotIssueUnenrolledSession(t *testing.T) {
	srv, store, _ := newTestServer(t)
	handler := routes.DefaultHandler
	handler.Store = mfaPolicyFailureStore{Store: store}
	defer func() { handler.Store = store }()
	w := reqFrom(srv, http.MethodPost, "/api/v1/auth/login", "", "10.54.0.1", []byte(`{"email":"dev@example.com","password":"password1234"}`))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("policy failure during login: %d", w.Code)
	}
	if len(w.Result().Cookies()) != 0 || strings.Contains(w.Body.String(), "access_token") {
		t.Fatal("unknown policy issued an unenrolled session")
	}
}

func TestMFAReadOnlyKeyCannotEnroll(t *testing.T) {
	srv, store, jwt := newTestServer(t)
	key := createToken(t, srv, jwt, []string{apikey.ScopeReadRepos})
	before, err := store.GetUserByEmail(t.Context(), "dev@example.com")
	if err != nil {
		t.Fatal(err)
	}
	w := request(srv, http.MethodPost, "/api/v1/auth/mfa/setup", key, []byte(`{}`))
	if w.Code != http.StatusForbidden {
		t.Errorf("read-only key enrolled owner: %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "otpauth_uri") || strings.Contains(w.Body.String(), "qr") {
		t.Error("API key obtained enrollment secret")
	}
	after, err := store.GetUserByID(t.Context(), before.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.TOTPSecret != before.TOTPSecret {
		t.Error("API key changed MFA enrollment")
	}
}

func TestMFAMachineCredentialMatrix(t *testing.T) {
	srv, store, jwt := newTestServer(t)
	keys := []string{
		createToken(t, srv, jwt, []string{apikey.ScopeReadRepos}),
		createToken(t, srv, jwt, []string{apikey.ScopeWriteCredentials}),
		createToken(t, srv, jwt, []string{apikey.ScopeAdmin}),
	}
	setup := request(srv, http.MethodPost, "/api/v1/auth/mfa/setup", jwt, []byte(`{}`))
	if setup.Code != http.StatusOK {
		t.Fatalf("human setup: %d", setup.Code)
	}
	secret := mfaData(t, setup.Body.Bytes())["secret"].(string)
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"code": code})
	before, err := store.GetUserByEmail(t.Context(), "dev@example.com")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		for _, action := range []string{"status", "setup", "activate", "disable"} {
			method := http.MethodPost
			if action == "status" {
				method = http.MethodGet
			}
			w := request(srv, method, "/api/v1/auth/mfa/"+action, key, body)
			if w.Code != http.StatusForbidden {
				t.Errorf("machine %s: %d", action, w.Code)
			}
			if strings.Contains(w.Body.String(), "recovery_codes") || strings.Contains(w.Body.String(), "otpauth_uri") {
				t.Error("machine obtained enrollment material")
			}
		}
	}
	after, err := store.GetUserByID(t.Context(), before.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.TOTPSecret != before.TOTPSecret || after.TOTPEnabled || after.MFARevision != before.MFARevision {
		t.Fatal("machine changed pending enrollment")
	}
	activated := request(srv, http.MethodPost, "/api/v1/auth/mfa/activate", jwt, body)
	if activated.Code != http.StatusOK {
		t.Fatalf("human activation: %d", activated.Code)
	}
	codes := mfaData(t, activated.Body.Bytes())["recovery_codes"].([]any)
	body, _ = json.Marshal(map[string]string{"code": codes[1].(string)})
	for _, key := range keys {
		w := request(srv, http.MethodPost, "/api/v1/auth/mfa/disable", key, body)
		if w.Code != http.StatusForbidden {
			t.Errorf("machine with valid recovery code disabled MFA: %d", w.Code)
		}
	}
	// Putting a machine key in a cookie must not turn it into a human session.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/mfa/disable", bytes.NewReader(body))
	req.AddCookie(&http.Cookie{Name: "wolf_token", Value: keys[0]})
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("machine cookie: %d", w.Code)
	}
	challenge := freshChallenge(t, srv, "10.54.1.1")
	loginBody, _ := json.Marshal(map[string]string{"mfa_token": challenge, "code": codes[0].(string)})
	login := reqFrom(srv, http.MethodPost, "/api/v1/auth/mfa/login", "", "10.54.1.2", loginBody)
	if login.Code != http.StatusOK {
		t.Fatalf("MFA login: %d", login.Code)
	}
	cookies := login.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("MFA login did not create a browser cookie")
	}
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/mfa/disable", bytes.NewReader(body))
	req.AddCookie(cookies[0])
	w = httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("human browser cookie could not disable MFA: %d", w.Code)
	}
}

func TestMFAMandatoryEnrollmentAndAutomation(t *testing.T) {
	srv, store, jwt := newTestServer(t)
	key := createToken(t, srv, jwt, []string{apikey.ScopeReadRepos})
	if err := store.SetSetting(t.Context(), "mfa_required", "true"); err != nil {
		t.Fatal(err)
	}
	login := reqFrom(srv, http.MethodPost, "/api/v1/auth/login", "", "10.54.2.1", []byte(`{"email":"dev@example.com","password":"password1234"}`))
	if login.Code != http.StatusOK || mfaData(t, login.Body.Bytes())["enrollment_required"] != true {
		t.Fatalf("mandatory login: %d", login.Code)
	}
	for _, path := range []string{"/api/v1/repos", "/api/v1/auth/tokens", "/api/v1/settings"} {
		if w := request(srv, http.MethodGet, path, jwt, nil); w.Code != http.StatusForbidden {
			t.Errorf("unenrolled human %s: %d", path, w.Code)
		}
	}
	if w := request(srv, http.MethodGet, "/api/v1/repos", key, nil); w.Code != http.StatusOK {
		t.Fatalf("scoped automation: %d", w.Code)
	}
	if w := request(srv, http.MethodGet, "/api/v1/auth/me", jwt, nil); w.Code != http.StatusOK {
		t.Fatalf("enrollment identity: %d", w.Code)
	}
	status := request(srv, http.MethodGet, "/api/v1/auth/mfa/status", jwt, nil)
	if status.Code != http.StatusOK || mfaData(t, status.Body.Bytes())["required"] != true {
		t.Fatalf("mandatory status: %d", status.Code)
	}
	_, codes := enrollMFAFixture(t, srv, jwt)
	if w := request(srv, http.MethodGet, "/api/v1/repos", jwt, nil); w.Code != http.StatusOK {
		t.Fatalf("enrolled human: %d", w.Code)
	}
	body, _ := json.Marshal(map[string]string{"code": codes[0]})
	if w := request(srv, http.MethodPost, "/api/v1/auth/mfa/disable", jwt, body); w.Code != http.StatusForbidden {
		t.Fatalf("mandatory disable: %d", w.Code)
	}
}

func TestMFAPolicyOutagePreservesSafeOperations(t *testing.T) {
	srv, store, jwt := newTestServer(t)
	key := createToken(t, srv, jwt, []string{apikey.ScopeReadRepos})
	handler := routes.DefaultHandler
	handler.Store = mfaPolicyFailureStore{Store: store}
	defer func() { handler.Store = store }()
	if w := request(srv, http.MethodGet, "/api/v1/repos", key, nil); w.Code != http.StatusOK {
		t.Fatalf("automation during policy outage: %d", w.Code)
	}
	if w := request(srv, http.MethodGet, "/api/v1/auth/me", jwt, nil); w.Code != http.StatusOK {
		t.Fatalf("identity during policy outage: %d", w.Code)
	}
	enrollMFAFixture(t, srv, jwt)
	if w := request(srv, http.MethodGet, "/api/v1/repos", jwt, nil); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("enrolled session bypassed unknown policy: %d", w.Code)
	}
	if w := request(srv, http.MethodPost, "/api/v1/auth/logout", jwt, []byte(`{}`)); w.Code != http.StatusOK {
		t.Fatalf("logout during policy outage: %d", w.Code)
	}
}

type mfaPolicySecondReadFailure struct {
	db.Store
	reads int
}

func (s *mfaPolicySecondReadFailure) GetSetting(ctx context.Context, key string) (string, error) {
	if key != "mfa_required" {
		return s.Store.GetSetting(ctx, key)
	}
	s.reads++
	if s.reads > 1 {
		return "", errors.New("private second policy read diagnostic")
	}
	return "false", nil
}
func TestMFADisableChecksPolicyAtAction(t *testing.T) {
	srv, store, jwt := newTestServer(t)
	_, codes := enrollMFAFixture(t, srv, jwt)
	before, err := store.GetUserByEmail(t.Context(), "dev@example.com")
	if err != nil {
		t.Fatal(err)
	}
	handler := routes.DefaultHandler
	handler.Store = &mfaPolicySecondReadFailure{Store: store}
	defer func() { handler.Store = store }()
	body, _ := json.Marshal(map[string]string{"code": codes[0]})
	w := request(srv, http.MethodPost, "/api/v1/auth/mfa/disable", jwt, body)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("second policy read failure: %d", w.Code)
	}
	after, err := store.GetUserByID(t.Context(), before.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.TOTPSecret != before.TOTPSecret || !after.TOTPEnabled || after.TOTPRecoveryCodes != before.TOTPRecoveryCodes {
		t.Fatal("unknown policy changed MFA state")
	}
}

func TestMFAPolicySettingsValidation(t *testing.T) {
	srv, store, jwt := newTestServer(t)
	if w := request(srv, http.MethodGet, "/api/v1/repos", jwt, nil); w.Code != http.StatusOK {
		t.Fatalf("absent policy: %d", w.Code)
	}
	for _, value := range []string{"", "false", " FaLsE "} {
		if err := store.SetSetting(t.Context(), "mfa_required", value); err != nil {
			t.Fatal(err)
		}
		if w := request(srv, http.MethodGet, "/api/v1/repos", jwt, nil); w.Code != http.StatusOK {
			t.Fatalf("optional policy %q: %d", value, w.Code)
		}
	}
	if err := store.SetSetting(t.Context(), "mfa_required", "invalid"); err != nil {
		t.Fatal(err)
	}
	if w := request(srv, http.MethodGet, "/api/v1/repos", jwt, nil); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("invalid stored policy: %d", w.Code)
	}
	if err := store.SetSetting(t.Context(), "mfa_required", "false"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSetting(t.Context(), "policy_test_sentinel", "original"); err != nil {
		t.Fatal(err)
	}
	w := request(srv, http.MethodPut, "/api/v1/settings", jwt, []byte(`{"mfa_required":"invalid","policy_test_sentinel":"changed"}`))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid policy write: %d", w.Code)
	}
	if value, err := store.GetSetting(t.Context(), "policy_test_sentinel"); err != nil || value != "original" {
		t.Fatalf("invalid policy partially wrote settings: %q %v", value, err)
	}
	w = request(srv, http.MethodPut, "/api/v1/settings", jwt, []byte(`{"mfa_required":" TrUe "}`))
	if w.Code != http.StatusOK {
		t.Fatalf("valid policy write: %d", w.Code)
	}
	if value, err := store.GetSetting(t.Context(), "mfa_required"); err != nil || value != "true" {
		t.Fatalf("policy not canonicalized: %q %v", value, err)
	}
	if w := request(srv, http.MethodGet, "/api/v1/repos", jwt, nil); w.Code != http.StatusForbidden {
		t.Fatalf("new mandatory policy not enforced: %d", w.Code)
	}
}

func TestMFAEnrollmentAllowlistMatchesExactOperation(t *testing.T) {
	_, store, jwt := newTestServer(t)
	if err := store.SetSetting(t.Context(), "mfa_required", "true"); err != nil {
		t.Fatal(err)
	}
	claims, err := auth.ValidateToken(jwt)
	if err != nil {
		t.Fatal(err)
	}
	guarded := routes.MFAEnrollmentGuard(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/api/v1/auth/me", http.StatusNoContent},
		{http.MethodPost, "/api/v1/auth/mfa/setup", http.StatusNoContent},
		{http.MethodGet, "/api/v1/unrelated/auth/me", http.StatusForbidden},
		{http.MethodPost, "/api/v1/unrelated/auth/mfa/setup", http.StatusForbidden},
		{http.MethodPut, "/api/v1/auth/me", http.StatusForbidden},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req = req.WithContext(context.WithValue(req.Context(), auth.UserContextKey, claims))
		w := httptest.NewRecorder()
		guarded.ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Errorf("%s %s: %d, want %d", tc.method, tc.path, w.Code, tc.want)
		}
	}
}

type mfaStateReadFailureStore struct{ db.Store }

func (mfaStateReadFailureStore) GetUserByID(context.Context, string) (*models.User, error) {
	return nil, errors.New("private MFA state diagnostic")
}
func TestMFAStateReadFailureIsUnavailable(t *testing.T) {
	srv, store, jwt := newTestServer(t)
	if err := store.SetSetting(t.Context(), "mfa_required", "true"); err != nil {
		t.Fatal(err)
	}
	handler := routes.DefaultHandler
	handler.Store = mfaStateReadFailureStore{Store: store}
	defer func() { handler.Store = store }()
	for _, path := range []string{"/api/v1/repos", "/api/v1/auth/mfa/status"} {
		w := request(srv, http.MethodGet, path, jwt, nil)
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("state read failure %s: %d", path, w.Code)
		}
		if strings.Contains(w.Body.String(), "private MFA state") {
			t.Error("state read error exposed private diagnostic")
		}
	}
}
func TestMFARegularHumanCanEnrollButMachineCannot(t *testing.T) {
	srv, store, _ := newTestServer(t)
	user := &models.User{ID: "regular-mfa", Email: "regular-mfa@example.test", PasswordHash: "unused", Role: models.RoleUser}
	if err := store.CreateUser(t.Context(), user); err != nil {
		t.Fatal(err)
	}
	pair, err := auth.GenerateToken(user.ID, user.Email)
	if err != nil {
		t.Fatal(err)
	}
	key := createToken(t, srv, pair.AccessToken, []string{apikey.ScopeReadRepos})
	if w := request(srv, http.MethodPost, "/api/v1/auth/mfa/setup", key, []byte(`{}`)); w.Code != http.StatusForbidden {
		t.Fatalf("regular owner's machine key: %d", w.Code)
	}
	if w := request(srv, http.MethodPost, "/api/v1/auth/mfa/setup", pair.AccessToken, []byte(`{}`)); w.Code != http.StatusOK {
		t.Fatalf("regular human enrollment: %d", w.Code)
	}
}
