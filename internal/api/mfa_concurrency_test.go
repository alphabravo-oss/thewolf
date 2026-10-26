package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/alphabravocompany/thewolf/internal/api"
	"github.com/alphabravocompany/thewolf/internal/api/routes"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
)

func enrollMFAFixture(t *testing.T, srv *api.Server, jwt string) (string, []string) {
	t.Helper()
	w := request(srv, http.MethodPost, "/api/v1/auth/mfa/setup", jwt, []byte(`{}`))
	if w.Code != http.StatusOK {
		t.Fatalf("setup: %d", w.Code)
	}
	secret := mfaData(t, w.Body.Bytes())["secret"].(string)
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"code": code})
	w = request(srv, http.MethodPost, "/api/v1/auth/mfa/activate", jwt, body)
	if w.Code != http.StatusOK {
		t.Fatalf("activate: %d", w.Code)
	}
	var codes []string
	for _, value := range mfaData(t, w.Body.Bytes())["recovery_codes"].([]any) {
		codes = append(codes, value.(string))
	}
	return secret, codes
}

type mfaReadBarrierStore struct {
	db.Store
	user  string
	reads atomic.Int32
	ready chan struct{}
}

func (s *mfaReadBarrierStore) GetUserByID(ctx context.Context, id string) (*models.User, error) {
	user, err := s.Store.GetUserByID(ctx, id)
	if err == nil && id == s.user {
		if n := s.reads.Add(1); n <= 2 {
			if n == 2 {
				close(s.ready)
			}
			select {
			case <-s.ready:
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(3 * time.Second):
				return nil, errors.New("MFA read barrier timed out")
			}
		}
	}
	return user, err
}

func TestMFARecoveryConcurrentConsumption(t *testing.T) {
	for _, different := range []bool{false, true} {
		t.Run(fmt.Sprintf("different_codes=%v", different), func(t *testing.T) {
			srv, store, jwt := newTestServer(t)
			_, codes := enrollMFAFixture(t, srv, jwt)
			before, err := store.GetUserByEmail(t.Context(), "dev@example.com")
			if err != nil {
				t.Fatal(err)
			}
			challenge := freshChallenge(t, srv, "10.50.0.10")
			handler := routes.DefaultHandler
			handler.Store = &mfaReadBarrierStore{Store: store, user: before.ID, ready: make(chan struct{})}
			defer func() { handler.Store = store }()
			selected := []string{codes[0], codes[0]}
			if different {
				selected[1] = codes[1]
			}
			type result struct {
				index    int
				response *httptest.ResponseRecorder
			}
			results := make(chan result, 2)
			for i, code := range selected {
				body, _ := json.Marshal(map[string]string{"mfa_token": challenge, "code": code})
				go func() {
					results <- result{i, reqFrom(srv, http.MethodPost, "/api/v1/auth/mfa/login", "", fmt.Sprintf("10.50.0.%d", i+1), body)}
				}()
			}
			succeeded, conflicted, failedCode := 0, 0, ""
			for range 2 {
				select {
				case got := <-results:
					w := got.response
					switch w.Code {
					case http.StatusOK:
						succeeded++
					case http.StatusConflict:
						conflicted++
						failedCode = selected[got.index]
					default:
						t.Errorf("concurrent login: %d", w.Code)
					}
					if w.Code != http.StatusOK && (len(w.Result().Cookies()) != 0 || strings.Contains(w.Body.String(), "access_token")) {
						t.Error("failed request issued credentials")
					}
				case <-time.After(5 * time.Second):
					t.Fatal("concurrent requests did not finish")
				}
			}
			if succeeded != 1 || conflicted != 1 {
				t.Fatalf("same snapshot produced successes=%d conflicts=%d, want 1/1", succeeded, conflicted)
			}
			current, err := store.GetUserByID(t.Context(), before.ID)
			if err != nil {
				t.Fatal(err)
			}
			var remaining []string
			if err := json.Unmarshal([]byte(current.TOTPRecoveryCodes), &remaining); err != nil {
				t.Fatal(err)
			}
			if len(remaining) != len(codes)-1 || !current.MFAEnabled() || current.TOTPSecret != before.TOTPSecret {
				t.Fatal("MFA state corrupted by concurrent consumption")
			}
			challenge = freshChallenge(t, srv, "10.50.0.12")
			body, _ := json.Marshal(map[string]string{"mfa_token": challenge, "code": failedCode})
			retry := reqFrom(srv, http.MethodPost, "/api/v1/auth/mfa/login", "", "10.50.0.11", body)
			want := http.StatusUnauthorized
			if different {
				want = http.StatusOK
			}
			if retry.Code != want {
				t.Fatalf("retry after conflict: %d, want %d", retry.Code, want)
			}
			for i := 0; i < 2; i++ {
				if !different && i == 1 {
					break
				}
				body, _ = json.Marshal(map[string]string{"mfa_token": challenge, "code": codes[i]})
				if w := reqFrom(srv, http.MethodPost, "/api/v1/auth/mfa/login", "", fmt.Sprintf("10.50.0.%d", i+20), body); w.Code != http.StatusUnauthorized {
					t.Errorf("consumed code restored: %d", w.Code)
				}
			}
			current, err = store.GetUserByID(t.Context(), before.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, code := range selected {
				if strings.Contains(current.TOTPRecoveryCodes, auth.HashRecoveryCode(code)) {
					t.Error("consumed recovery hash retained")
				}
			}
		})
	}
}

func TestMFAActivationCannotReplaceActiveRecoveryCodes(t *testing.T) {
	srv, store, jwt := newTestServer(t)
	secret, _ := enrollMFAFixture(t, srv, jwt)
	before, err := store.GetUserByEmail(t.Context(), "dev@example.com")
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"code": code})
	w := request(srv, http.MethodPost, "/api/v1/auth/mfa/activate", jwt, body)
	if w.Code != http.StatusConflict {
		t.Errorf("already active enrollment: %d", w.Code)
	}
	after, err := store.GetUserByID(t.Context(), before.ID)
	if err != nil {
		t.Fatal(err)
	}
	if before.TOTPRecoveryCodes != after.TOTPRecoveryCodes {
		t.Error("reactivation replaced unused recovery codes")
	}
}

type mfaWriteFailureStore struct {
	db.Store
	failure error
}

func (s mfaWriteFailureStore) UpdateUserMFA(context.Context, *models.User, *models.User) error {
	return s.failure
}

func (s mfaWriteFailureStore) CompleteMFALogin(context.Context, string, *models.User, *models.User, *models.AuthSession, *models.AuthSession) error {
	return s.failure
}

func TestMFAStateWriteFailuresDoNotIssueCredentials(t *testing.T) {
	for _, action := range []string{"setup", "activate", "disable", "recovery login", "totp login", "reset"} {
		t.Run(action, func(t *testing.T) {
			srv, store, jwt := newTestServer(t)
			secret, codes := enrollMFAFixture(t, srv, jwt)
			user, err := store.GetUserByEmail(t.Context(), "dev@example.com")
			if err != nil {
				t.Fatal(err)
			}
			initial := *user
			if action == "setup" {
				user.TOTPSecret = ""
				user.TOTPEnabled = false
				user.TOTPRecoveryCodes = ""
			}
			if action == "activate" {
				user.TOTPEnabled = false
				user.TOTPRecoveryCodes = ""
			}
			if err := store.UpdateUserMFA(t.Context(), user, &initial); err != nil {
				t.Fatal(err)
			}
			challenge, err := auth.GenerateMFAChallenge(user.ID, user.Email, user.CredentialVersion)
			if err != nil {
				t.Fatal(err)
			}
			code := codes[0]
			if action == "activate" || action == "totp login" {
				code, err = totp.GenerateCode(secret, time.Now().Add(30*time.Second))
				if err != nil {
					t.Fatal(err)
				}
			}
			body, _ := json.Marshal(map[string]string{"code": code, "mfa_token": challenge})
			path, bearer := "/api/v1/auth/mfa/"+action, jwt
			if strings.HasSuffix(action, "login") {
				path = "/api/v1/auth/mfa/login"
				bearer = ""
			}
			if action == "reset" {
				path = "/api/v1/users/" + user.ID + "/mfa/reset"
			}
			handler := routes.DefaultHandler
			defer func() { handler.Store = store }()
			for i, failure := range []error{db.ErrMFAChanged, errors.New("private database diagnostic")} {
				handler.Store = mfaWriteFailureStore{Store: store, failure: failure}
				w := reqFrom(srv, http.MethodPost, path, bearer, fmt.Sprintf("10.51.0.%d", i+1), body)
				want := http.StatusConflict
				if i == 1 {
					want = http.StatusInternalServerError
				}
				if w.Code != want {
					t.Errorf("write failure: %d, want %d: %s", w.Code, want, w.Body.String())
				}
				if len(w.Result().Cookies()) != 0 {
					t.Error("failed MFA write issued cookie")
				}
				for _, sensitive := range []string{"access_token", "refresh_token", "recovery_codes", secret, "private database diagnostic"} {
					if strings.Contains(w.Body.String(), sensitive) {
						t.Errorf("failed write exposed %s", sensitive)
					}
				}
				after, err := store.GetUserByID(t.Context(), user.ID)
				if err != nil {
					t.Fatal(err)
				}
				if after.TOTPSecret != user.TOTPSecret || after.TOTPEnabled != user.TOTPEnabled || after.TOTPRecoveryCodes != user.TOTPRecoveryCodes {
					t.Error("failed write changed stored MFA state")
				}
			}
		})
	}
}
