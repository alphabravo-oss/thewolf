package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/auth/apikey"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestUpdateUserScannerSupplyChainAccessValidatesPresets(t *testing.T) {
	store, err := db.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	SetHandler(store, nil)

	admin := &models.User{ID: uuid.NewString(), Email: "admin@example.test", PasswordHash: "hash", Role: models.RoleAdmin}
	operatorEncoded, _, _ := apikey.EncodeScannerPersonas([]string{apikey.ScannerPersonaOperator})
	user := &models.User{
		ID: uuid.NewString(), Email: "user@example.test", PasswordHash: "hash", Role: models.RoleUser,
		ScannerSupplyChainPersonas: operatorEncoded,
	}
	for _, candidate := range []*models.User{admin, user} {
		if err := store.CreateUser(context.Background(), candidate); err != nil {
			t.Fatal(err)
		}
	}

	request := func(actorID, targetID, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPut, "/api/v1/users/"+targetID+"/scanner-supply-chain-access", bytes.NewBufferString(body))
		routeContext := chi.NewRouteContext()
		routeContext.URLParams.Add("id", targetID)
		ctx := context.WithValue(req.Context(), chi.RouteCtxKey, routeContext)
		ctx = context.WithValue(ctx, auth.UserContextKey, &auth.Claims{UserID: actorID, Role: models.RoleAdmin})
		recorder := httptest.NewRecorder()
		UpdateUserScannerSupplyChainAccess(recorder, req.WithContext(ctx))
		return recorder
	}

	success := request(admin.ID, user.ID, `{"personas":["scanner_operator","release_approver"]}`)
	if success.Code != http.StatusOK {
		t.Fatalf("update access = %d: %s", success.Code, success.Body.String())
	}
	var responseBody struct {
		Data struct {
			Personas []string `json:"scanner_supply_chain_personas"`
			Scopes   []string `json:"scanner_supply_chain_scopes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(success.Body.Bytes(), &responseBody); err != nil {
		t.Fatal(err)
	}
	if len(responseBody.Data.Personas) != 2 ||
		!apikey.ScopeSet(responseBody.Data.Scopes).Has(apikey.ScopeOperateScannerSupplyChain) ||
		!apikey.ScopeSet(responseBody.Data.Scopes).Has(apikey.ScopeApproveScannerReleases) {
		t.Fatalf("access response = %#v", responseBody.Data)
	}

	for _, body := range []string{
		`{"personas":["admin:scanner-supply-chain"]}`,
		`{"personas":["scanner_operator"],"scopes":["admin"]}`,
		`{}`,
		`{"personas":["viewer"]} {}`,
	} {
		if got := request(admin.ID, user.ID, body); got.Code != http.StatusBadRequest {
			t.Errorf("invalid body %s = %d: %s", body, got.Code, got.Body.String())
		}
	}
	if got := request(user.ID, user.ID, `{"personas":["viewer"]}`); got.Code != http.StatusBadRequest {
		t.Fatalf("self assignment = %d: %s", got.Code, got.Body.String())
	}
	if got := request(user.ID, admin.ID, `{"personas":["viewer"]}`); got.Code != http.StatusBadRequest {
		t.Fatalf("admin assignment = %d: %s", got.Code, got.Body.String())
	}
}

func TestUpdateUserRolePreservesAssignmentExceptAdminDemotion(t *testing.T) {
	store, err := db.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	SetHandler(store, nil)
	actor := &models.User{ID: uuid.NewString(), Email: "actor@example.test", PasswordHash: "hash", Role: models.RoleAdmin}
	encoded, _, _ := apikey.EncodeScannerPersonas([]string{apikey.ScannerPersonaOperator, apikey.ScannerPersonaApprover})
	target := &models.User{
		ID: uuid.NewString(), Email: "target@example.test", PasswordHash: "hash", Role: models.RoleUser,
		ScannerSupplyChainPersonas: encoded,
	}
	for _, user := range []*models.User{actor, target} {
		if err := store.CreateUser(context.Background(), user); err != nil {
			t.Fatal(err)
		}
	}

	setRole := func(role string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPut, "/api/v1/users/"+target.ID+"/role", bytes.NewBufferString(`{"role":"`+role+`"}`))
		routeContext := chi.NewRouteContext()
		routeContext.URLParams.Add("id", target.ID)
		ctx := context.WithValue(req.Context(), chi.RouteCtxKey, routeContext)
		ctx = context.WithValue(ctx, auth.UserContextKey, &auth.Claims{UserID: actor.ID, Role: models.RoleAdmin})
		recorder := httptest.NewRecorder()
		UpdateUserRole(recorder, req.WithContext(ctx))
		if recorder.Code != http.StatusOK {
			t.Fatalf("set role %s = %d: %s", role, recorder.Code, recorder.Body.String())
		}
	}

	setRole(models.RoleUser)
	got, _ := store.GetUserByID(context.Background(), target.ID)
	if got.ScannerSupplyChainPersonas != encoded {
		t.Fatalf("user no-op role update changed personas to %q", got.ScannerSupplyChainPersonas)
	}
	setRole(models.RoleAdmin)
	got, _ = store.GetUserByID(context.Background(), target.ID)
	if got.ScannerSupplyChainPersonas != encoded {
		t.Fatalf("promotion changed stored personas to %q", got.ScannerSupplyChainPersonas)
	}
	setRole(models.RoleUser)
	got, _ = store.GetUserByID(context.Background(), target.ID)
	personas, err := apikey.DecodeScannerPersonas(got.ScannerSupplyChainPersonas)
	if err != nil || len(personas) != 1 || personas[0] != apikey.ScannerPersonaViewer {
		t.Fatalf("demotion personas = %v, err %v", personas, err)
	}
}
