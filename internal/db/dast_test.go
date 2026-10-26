package db

import (
	"errors"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestDASTApprovalRevision(t *testing.T) {
	for _, backend := range []string{"sqlite", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store Store
			if backend == "postgres" {
				store = newClusterPostgresStore(t)
			} else {
				s, err := NewSQLite(":memory:")
				if err != nil {
					t.Fatal(err)
				}
				store = s
				t.Cleanup(func() { _ = s.Close() })
			}
			ctx := t.Context()
			if err := store.CreateUser(ctx, &models.User{ID: "dast-owner", Email: "dast@example.test", PasswordHash: "unused"}); err != nil {
				t.Fatal(err)
			}
			if err := store.CreateRepo(ctx, &models.Repo{ID: "dast-repo", UserID: "dast-owner", Name: "DAST", SourcePath: t.TempDir()}); err != nil {
				t.Fatal(err)
			}
			target, err := store.GetDASTTarget(ctx, "dast-repo")
			if err != nil {
				t.Fatal(err)
			}
			if target.Approved() || target.Mode != "baseline" {
				t.Fatal("unsafe default")
			}
			target.URL = "https://example.test"
			target.AuthSecretID = "reference-only"
			if err := store.SaveDASTTarget(ctx, target, false); err != nil {
				t.Fatal(err)
			}
			stale := *target
			target.ApprovedBy = "admin"
			if err := store.SaveDASTTarget(ctx, target, true); err != nil {
				t.Fatal(err)
			}
			if err := store.SaveDASTTarget(ctx, &stale, true); !errors.Is(err, ErrDASTRevision) {
				t.Fatalf("stale approval: %v", err)
			}
			for _, field := range []string{"url", "mode", "spec", "secret"} {
				switch field {
				case "url":
					target.URL = "https://changed.test"
				case "mode":
					target.Mode = "full"
				case "spec":
					target.APISpecURL = "https://changed.test/openapi.json"
				case "secret":
					target.AuthSecretID = "new-reference"
				}
				if err := store.SaveDASTTarget(ctx, target, false); err != nil {
					t.Fatal(err)
				}
				target, err = store.GetDASTTarget(ctx, target.RepoID)
				if err != nil {
					t.Fatal(err)
				}
				if target.Approved() || target.ApprovedBy != "" || target.AllowPrivate || target.ActiveApproved {
					t.Fatalf("%s edit retained approval", field)
				}
				target.ApprovedBy = "admin"
				target.AllowPrivate = true
				target.ActiveApproved = true
				if err := store.SaveDASTTarget(ctx, target, true); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
