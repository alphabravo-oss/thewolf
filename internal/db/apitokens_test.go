package db

import (
	"context"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/auth/apikey"
	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestAPITokenLifecycle(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	tok := &models.APIToken{
		ID:          "tok-1",
		UserID:      "user-1",
		Name:        "ci-pipeline",
		TokenHash:   "hash-abc",
		TokenPrefix: "wolf_abc",
		ScopeList:   apikey.ScopeSet{apikey.ScopeReadScans, apikey.ScopeWriteScans},
	}
	if err := store.CreateAPIToken(ctx, tok); err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}

	// Lookup by hash decodes scopes back to a usable set.
	got, err := store.GetAPITokenByHash(ctx, "hash-abc")
	if err != nil {
		t.Fatalf("GetAPITokenByHash: %v", err)
	}
	if got.UserID != "user-1" || got.Name != "ci-pipeline" {
		t.Errorf("unexpected token: %+v", got)
	}
	if !apikey.ScopeSet(got.ScopeList).Has(apikey.ScopeWriteScans) {
		t.Errorf("scopes did not round-trip: %v", got.ScopeList)
	}
	if got.RevokedAt != nil {
		t.Error("new token should not be revoked")
	}

	// Touch updates last_used_at.
	if err := store.TouchAPIToken(ctx, "tok-1"); err != nil {
		t.Fatalf("TouchAPIToken: %v", err)
	}
	got, _ = store.GetAPITokenByID(ctx, "tok-1")
	if got.LastUsedAt == nil {
		t.Error("TouchAPIToken did not set last_used_at")
	}

	// List returns the user's tokens.
	list, err := store.ListAPITokensByUser(ctx, "user-1")
	if err != nil || len(list) != 1 {
		t.Fatalf("ListAPITokensByUser: got %d tokens, err %v", len(list), err)
	}

	// Revoke sets revoked_at.
	if err := store.RevokeAPIToken(ctx, "tok-1"); err != nil {
		t.Fatalf("RevokeAPIToken: %v", err)
	}
	got, _ = store.GetAPITokenByID(ctx, "tok-1")
	if got.RevokedAt == nil {
		t.Error("RevokeAPIToken did not set revoked_at")
	}

	// Unknown hash is a not-found error, not a nil token.
	if _, err := store.GetAPITokenByHash(ctx, "does-not-exist"); err == nil {
		t.Error("expected error for unknown token hash")
	}
}

func TestAuditLogAppendAndList(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	tokenID := "tok-9"
	for i := 0; i < 3; i++ {
		entry := &models.AuditLogEntry{
			ID:         "audit-" + time.Now().Format("150405.000000000"),
			TokenID:    &tokenID,
			UserID:     "user-1",
			Action:     "create",
			Method:     "POST",
			Path:       "/api/v1/scans",
			StatusCode: 201,
		}
		if err := store.AppendAuditLog(ctx, entry); err != nil {
			t.Fatalf("AppendAuditLog: %v", err)
		}
		time.Sleep(time.Millisecond)
	}

	entries, err := store.ListAuditLog(ctx, 10)
	if err != nil {
		t.Fatalf("ListAuditLog: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 audit entries, got %d", len(entries))
	}
	if entries[0].Method != "POST" || entries[0].StatusCode != 201 {
		t.Errorf("unexpected audit entry: %+v", entries[0])
	}
	if entries[0].TokenID == nil || *entries[0].TokenID != tokenID {
		t.Error("token_id did not round-trip")
	}
}

func TestAuditLogNullTokenID(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	// A JWT (UI) request records a nil token_id.
	if err := store.AppendAuditLog(ctx, &models.AuditLogEntry{
		ID: "audit-jwt", UserID: "user-1", Action: "update",
		Method: "PUT", Path: "/api/v1/settings", StatusCode: 200,
	}); err != nil {
		t.Fatalf("AppendAuditLog: %v", err)
	}
	entries, err := store.ListAuditLog(ctx, 10)
	if err != nil || len(entries) != 1 {
		t.Fatalf("ListAuditLog: %d entries, err %v", len(entries), err)
	}
	if entries[0].TokenID != nil {
		t.Errorf("expected nil token_id for a JWT request, got %v", *entries[0].TokenID)
	}
}
