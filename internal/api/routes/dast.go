package routes

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/dast"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/secrets"
	"github.com/go-chi/chi/v5"
)

type updateDASTRequest struct {
	URL          string `json:"url"`
	Mode         string `json:"mode"`
	APISpecURL   string `json:"api_spec_url"`
	APIFormat    string `json:"api_format"`
	AuthSecretID string `json:"auth_secret_id"`
	Revision     int64  `json:"revision"`
}

type approveDASTRequest struct {
	Revision      int64 `json:"revision"`
	AllowPrivate  bool  `json:"allow_private"`
	ApproveActive bool  `json:"approve_active"`
}

func RepoDASTTarget(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, 401, "unauthorized", "not authenticated")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, 500, "server_error", "handler not initialized")
		return
	}
	repo, ok := loadRepoForCaller(w, r, h.Store, chi.URLParam(r, "id"), claims)
	if !ok {
		return
	}
	t, err := h.Store.GetDASTTarget(r.Context(), repo.ID)
	if err != nil {
		response.WriteError(w, 500, "server_error", "failed to read DAST target")
		return
	}
	approve := r.Method == http.MethodPost
	if r.Method != http.MethodGet {
		if approve {
			if !claims.IsAdmin() {
				response.WriteError(w, 403, "forbidden", "only administrators can approve DAST targets")
				return
			}
			var req approveDASTRequest
			if !scannerDecode(w, r, &req) {
				return
			}
			if req.Revision != t.Revision {
				dastWriteError(w, db.ErrDASTRevision)
				return
			}
			if t.URL == "" {
				dastWriteError(w, fmt.Errorf("configure a target before approval"))
				return
			}
			if t.Mode == "full" && !req.ApproveActive {
				dastWriteError(w, fmt.Errorf("full scans require explicit active-attack approval for this target"))
				return
			}
			t.ApprovedBy, t.AllowPrivate, t.ActiveApproved = claims.UserID, req.AllowPrivate, req.ApproveActive && t.Mode == "full"
		} else {
			if !canModifyOwned(claims, repo.UserID) {
				response.WriteError(w, 403, "forbidden", "repository is read-only")
				return
			}
			var req updateDASTRequest
			if !scannerDecode(w, r, &req) {
				return
			}
			t = &models.DASTTarget{RepoID: repo.ID, URL: req.URL, Mode: req.Mode, APISpecURL: req.APISpecURL, APIFormat: req.APIFormat, AuthSecretID: req.AuthSecretID, Revision: req.Revision}
		}
		if err := dast.ValidateSettings(t); err != nil {
			dastWriteError(w, err)
			return
		}
		if t.URL != "" {
			// Saving a private target requests approval; only approval grants it.
			allowPrivate := !approve || t.AllowPrivate
			if _, err := dast.Resolve(r.Context(), t.URL, allowPrivate); err != nil {
				dastWriteError(w, err)
				return
			}
		}
		if _, err := dastHeaders(r.Context(), h.Store, repo, t); err != nil {
			dastWriteError(w, err)
			return
		}
		if err := h.Store.SaveDASTTarget(r.Context(), t, approve); err != nil {
			dastWriteError(w, err)
			return
		}
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: t})
}

func dastWriteError(w http.ResponseWriter, err error) {
	if errors.Is(err, db.ErrDASTRevision) {
		response.WriteError(w, 409, "dast_revision_conflict", err.Error())
		return
	}
	response.WriteError(w, 400, "dast_target_invalid", err.Error())
}

func dastHeaders(ctx context.Context, store db.Store, repo *models.Repo, t *models.DASTTarget) (map[string]string, error) {
	if t.AuthSecretID == "" {
		return nil, nil
	}
	secret, err := store.GetSecretByID(ctx, t.AuthSecretID)
	if err != nil || secret.UserID != repo.UserID || secret.KeyType != models.KeyTypeCustom {
		return nil, fmt.Errorf("select a custom auth-header secret owned by the repository owner")
	}
	value, err := secrets.Decrypt(secret.EncryptedValue)
	if err != nil {
		return nil, fmt.Errorf("cannot decrypt DAST auth-header secret")
	}
	return dast.Headers(value)
}

func resolveDAST(ctx context.Context, store db.Store, repoID string) (*models.DASTExecution, error) {
	t, err := store.GetDASTTarget(ctx, repoID)
	if err != nil {
		return nil, fmt.Errorf("read approved DAST target: %w", err)
	}
	if !t.Approved() {
		return nil, &plugin.NotApplicableError{Reason: "no approved repository DAST target"}
	}
	if err := dast.ValidateSettings(t); err != nil {
		return nil, err
	}
	if _, err := dast.Resolve(ctx, t.URL, t.AllowPrivate); err != nil {
		return nil, err
	}
	repo, err := store.GetRepoByID(ctx, repoID)
	if err != nil {
		return nil, err
	}
	headers, err := dastHeaders(ctx, store, repo, t)
	if err != nil {
		return nil, err
	}
	return &models.DASTExecution{Target: *t, Headers: headers}, nil
}
