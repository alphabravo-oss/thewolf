package routes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/artifacts"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/db"
	gitpkg "github.com/alphabravocompany/thewolf/internal/fix/git"
	"github.com/alphabravocompany/thewolf/internal/fix/writability"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/remote"
	"github.com/alphabravocompany/thewolf/internal/scan/detector"
	"github.com/alphabravocompany/thewolf/internal/scantarget"
	"github.com/alphabravocompany/thewolf/internal/secrets"
	"github.com/alphabravocompany/thewolf/internal/wolflog"
)

var listRemoteBranches = gitpkg.ListRemoteBranches

type createRepoRequest struct {
	Name               string            `json:"name"`
	SourceType         models.SourceType `json:"source_type"`
	SourcePath         string            `json:"source_path"`
	RemoteNodeID       *string           `json:"remote_node_id,omitempty"`
	RemotePath         string            `json:"remote_path,omitempty"`
	CredentialSecretID string            `json:"credential_secret_id,omitempty"`
	DefaultBranch      string            `json:"default_branch"`
}

// createRepoResult is the CreateRepo response body. It embeds the repo so
// existing clients still read `data.id`, `data.name`, etc. unchanged, and
// adds `deduplicated` — true when an add request matched an existing repo
// and we returned that one instead of creating a duplicate row.
type createRepoResult struct {
	*models.Repo
	Deduplicated bool `json:"deduplicated,omitempty"`
}

// normalizeSourcePath trims a single trailing slash so "/repos/x" and
// "/repos/x/" are treated as the same repo for dedup purposes.
func normalizeSourcePath(p string) string {
	if len(p) > 1 && strings.HasSuffix(p, "/") {
		return p[:len(p)-1]
	}
	return p
}

type updateRepoRequest struct {
	Name          string `json:"name"`
	DefaultBranch string `json:"default_branch"`
}

func ListRepos(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	var (
		repos []models.Repo
		err   error
	)
	if fleetVisible(r.Context(), h.Store, claims.UserID) {
		repos, err = h.Store.ListAllRepos(r.Context())
	} else {
		repos, err = h.Store.ListReposByUser(r.Context(), claims.UserID)
	}
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list repos")
		return
	}

	response.WriteJSON(w, http.StatusOK, response.ListResponse{
		Data: repos,
		Meta: response.ListMeta{Total: len(repos), Page: 1, PerPage: len(repos)},
	})
}

func CreateRepo(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	var req createRepoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	if req.Name == "" || req.SourcePath == "" {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "name and source_path are required")
		return
	}
	if req.SourceType == "" {
		req.SourceType = models.SourceTypeLocal
	}
	if req.SourceType == models.SourceTypeSSH {
		if req.RemoteNodeID == nil || *req.RemoteNodeID == "" {
			response.WriteError(w, http.StatusBadRequest, "validation_error", "remote_node_id is required for ssh repos")
			return
		}
		if req.RemotePath == "" {
			req.RemotePath = req.SourcePath
		}
		if req.RemotePath == "" {
			response.WriteError(w, http.StatusBadRequest, "validation_error", "remote_path is required for ssh repos")
			return
		}
		node, err := h.Store.GetRemoteNodeByID(r.Context(), *req.RemoteNodeID)
		if err != nil {
			response.WriteError(w, http.StatusBadRequest, "validation_error", "remote_node_id does not reference a configured node")
			return
		}
		if !canModifyOwned(claims, node.UserID) {
			response.WriteError(w, http.StatusForbidden, "forbidden", "remote_node_id does not belong to current user")
			return
		}
		req.SourcePath = req.RemotePath
	}
	if req.SourceType == models.SourceTypeGitHub {
		// Reject malformed sources up front so the failure surfaces at
		// create time, not on the first scan attempt.
		if _, _, err := scantarget.ParseGitHubSource(req.SourcePath); err != nil {
			response.WriteError(w, http.StatusBadRequest, "validation_error", err.Error())
			return
		}
		if req.CredentialSecretID != "" && !repoCredentialSecretAllowed(
			w, r, h, claims.UserID, req.CredentialSecretID, models.KeyTypeGitHubToken,
		) {
			return
		}
	}
	if req.DefaultBranch == "" {
		req.DefaultBranch = "main"
	}

	// Dedup: if the user already has a repo at this path, return that one
	// instead of creating a duplicate. Match on normalized source_path
	// (trailing-slash insensitive), scoped to the requesting user.
	if existing, err := h.Store.ListReposByUser(r.Context(), claims.UserID); err == nil {
		want := normalizeSourcePath(req.SourcePath)
		for i := range existing {
			sameNode := true
			if req.SourceType == models.SourceTypeSSH {
				sameNode = existing[i].RemoteNodeID != nil && req.RemoteNodeID != nil && *existing[i].RemoteNodeID == *req.RemoteNodeID
			}
			if existing[i].SourceType == req.SourceType && sameNode && normalizeSourcePath(existing[i].SourcePath) == want {
				if req.CredentialSecretID != "" && existing[i].CredentialSecretID != req.CredentialSecretID {
					existing[i].CredentialSecretID = req.CredentialSecretID
					_ = h.Store.UpdateRepo(r.Context(), &existing[i])
				}
				response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
					Data: createRepoResult{Repo: &existing[i], Deduplicated: true},
				})
				return
			}
		}
	}

	now := time.Now()
	repo := &models.Repo{
		ID:                 uuid.New().String(),
		UserID:             claims.UserID,
		Name:               req.Name,
		SourceType:         req.SourceType,
		SourcePath:         req.SourcePath,
		RemoteNodeID:       req.RemoteNodeID,
		RemotePath:         req.RemotePath,
		CredentialSecretID: req.CredentialSecretID,
		DefaultBranch:      req.DefaultBranch,
		CreatedAt:          now,
		UpdatedAt:          now,
	}

	if err := h.Store.CreateRepo(r.Context(), repo); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to create repo")
		return
	}

	// Folder model: every repo belongs to exactly one collection, so a new repo
	// lands in the owner's Default collection. The collection-detail "add repo"
	// flow then moves it into the chosen collection via SetRepoCollection.
	if defID, derr := ensureDefaultCollection(r.Context(), h.Store, claims.UserID); derr == nil {
		_ = h.Store.SetRepoCollection(r.Context(), repo.ID, defID)
	}

	// Run local detection asynchronously so the response isn't blocked.
	// SSH repos are detected when a scan prepares a local archive workspace.
	if repo.SourceType == models.SourceTypeLocal {
		go runDetection(h.Store, repo.ID, repo.SourcePath)
	}

	response.WriteJSON(w, http.StatusCreated, response.SuccessResponse{
		Data: createRepoResult{Repo: repo},
	})
}

func repoCredentialSecretAllowed(
	w http.ResponseWriter,
	r *http.Request,
	h *Handler,
	userID string,
	secretID string,
	keyType models.KeyType,
) bool {
	secretID = strings.TrimSpace(secretID)
	if secretID == "" {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "credential_secret_id cannot be empty")
		return false
	}
	secret, err := h.Store.GetSecretByID(r.Context(), secretID)
	if err != nil {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "credential_secret_id does not reference a configured secret")
		return false
	}
	if secret.UserID != userID {
		response.WriteError(w, http.StatusForbidden, "forbidden", "credential secret does not belong to current user")
		return false
	}
	if secret.KeyType != keyType {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "credential_secret_id must reference a github_token secret")
		return false
	}
	return true
}

func GetRepo(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	id := chi.URLParam(r, "id")
	repo, ok := loadRepoForCaller(w, r, h.Store, id, claims)
	if !ok {
		return
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: repo})
}

func UpdateRepo(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	id := chi.URLParam(r, "id")
	repo, ok := loadRepoForCaller(w, r, h.Store, id, claims)
	if !ok {
		return
	}
	if !canModifyOwned(claims, repo.UserID) {
		response.WriteError(w, http.StatusForbidden, "forbidden", "you can only modify repositories you created")
		return
	}

	var req updateRepoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	if req.Name != "" {
		repo.Name = req.Name
	}
	if req.DefaultBranch != "" {
		repo.DefaultBranch = req.DefaultBranch
	}
	repo.UpdatedAt = time.Now()

	if err := h.Store.UpdateRepo(r.Context(), repo); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to update repo")
		return
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: repo})
}

// prepareRepoForSync clones or refreshes a repo's source. Tests replace this
// so SyncRepo does not hit the network.
var prepareRepoForSync = func(ctx context.Context, store db.Store, repo *models.Repo, branch string) (scantarget.Prepared, error) {
	return (scantarget.Resolver{Store: store}).Sync(ctx, repo, branch)
}

// SetPrepareRepoForSyncForTest swaps the sync backend. The returned function
// restores the previous implementation.
func SetPrepareRepoForSyncForTest(fn func(context.Context, db.Store, *models.Repo, string) (scantarget.Prepared, error)) func() {
	prev := prepareRepoForSync
	if fn == nil {
		prepareRepoForSync = func(ctx context.Context, store db.Store, repo *models.Repo, branch string) (scantarget.Prepared, error) {
			return (scantarget.Resolver{Store: store}).Sync(ctx, repo, branch)
		}
	} else {
		prepareRepoForSync = fn
	}
	return func() { prepareRepoForSync = prev }
}

type syncRepoResult struct {
	*models.Repo
	Branch            string `json:"branch"`
	PreviousCommitSHA string `json:"previous_commit_sha,omitempty"`
	Changed           bool   `json:"changed"`
}

// SyncRepo handles POST /api/repos/{id}/sync — pull the latest commits into
// the local cache (or refresh recorded HEAD for local/SSH) without starting
// a scan. Language detection is refreshed when a local tree is available.
func SyncRepo(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	id := chi.URLParam(r, "id")
	repo, ok := loadRepoForCaller(w, r, h.Store, id, claims)
	if !ok {
		return
	}
	if !canModifyOwned(claims, repo.UserID) {
		response.WriteError(w, http.StatusForbidden, "forbidden", "you can only sync repositories you created")
		return
	}

	branch := strings.TrimSpace(r.URL.Query().Get("branch"))
	if branch == "" {
		branch = repo.DefaultBranch
	}

	prepared, err := prepareRepoForSync(r.Context(), h.Store, repo, branch)
	if err != nil {
		wolflog.Warn().Err(err).Str("repo_id", id).Str("branch", branch).Msg("repo sync failed")
		status := http.StatusBadGateway
		code := "repo_sync_failed"
		if repo.SourceType == models.SourceTypeLocal {
			status = http.StatusBadRequest
			code = "validation_error"
		}
		response.WriteError(w, status, code, "failed to sync repo: "+err.Error())
		return
	}
	if prepared.Cleanup != nil {
		defer prepared.Cleanup()
	}

	previous := repo.LastCommitSHA
	repo.LastCommitSHA = prepared.CommitSHA
	if prepared.DirtyState != "" {
		repo.LastDirtyState = prepared.DirtyState
	}
	repo.UpdatedAt = time.Now()
	if err := h.Store.UpdateRepo(r.Context(), repo); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to record synced commit")
		return
	}

	if prepared.Path != "" && repo.SourceType != models.SourceTypeSSH {
		applyRepoDetection(r.Context(), h.Store, repo.ID, prepared.Path)
	}

	updated, err := h.Store.GetRepoByID(r.Context(), repo.ID)
	if err != nil {
		updated = repo
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: syncRepoResult{
			Repo:              updated,
			Branch:            branch,
			PreviousCommitSHA: previous,
			Changed:           previous != prepared.CommitSHA,
		},
	})
}

func DeleteRepo(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	id := chi.URLParam(r, "id")
	repo, err := h.Store.GetRepoByID(r.Context(), id)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "repo not found")
		return
	}
	if !canModifyOwned(claims, repo.UserID) {
		response.WriteError(w, http.StatusForbidden, "forbidden", "you can only delete repositories you created")
		return
	}

	purge := wantPurgeRecords(r)
	var scanIDs []string
	if purge {
		scanIDs, err = h.Store.DeleteRepoCascade(r.Context(), id)
		if err != nil {
			wolflog.Error().Err(err).Str("repo_id", id).Msg("delete repo cascade failed")
			response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to delete repo: "+err.Error())
			return
		}
		if len(scanIDs) > 0 && artifacts.Global != nil {
			go artifacts.Global.DeleteScans(scanIDs)
		}
	} else {
		if err := h.Store.DeleteRepoKeepHistory(r.Context(), id); err != nil {
			if errors.Is(err, db.ErrHasScanRecords) {
				response.WriteError(w, http.StatusConflict, "records_exist",
					"this repo has scan records; pass purge=true to delete them as well")
				return
			}
			wolflog.Error().Err(err).Str("repo_id", id).Msg("delete repo failed")
			response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to delete repo: "+err.Error())
			return
		}
	}

	wolflog.Info().Str("repo_id", id).Str("repo_name", repo.Name).Int("scans_deleted", len(scanIDs)).Bool("purge", purge).Msg("repo deleted")

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]interface{}{
		"message":       "repo deleted",
		"scans_deleted": len(scanIDs),
		"purged":        purge,
	}})
}

// ListRepoBranches handles GET /api/repos/{id}/branches — returns available git branches.
func ListRepoBranches(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	id := chi.URLParam(r, "id")
	repo, err := h.Store.GetRepoByID(r.Context(), id)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "repo not found")
		return
	}
	if repo.SourceType == models.SourceTypeSSH && repo.RemoteNodeID != nil {
		node, nerr := h.Store.GetRemoteNodeByID(r.Context(), *repo.RemoteNodeID)
		if nerr != nil {
			response.WriteError(w, http.StatusNotFound, "not_found", "remote node not found")
			return
		}
		if !canModifyOwned(claims, node.UserID) {
			response.WriteError(w, http.StatusForbidden, "forbidden", "remote node does not belong to current user")
			return
		}
	}

	branches, current, err := repoBranchList(r.Context(), h, repo)
	if err != nil {
		response.WriteError(w, http.StatusBadGateway, "branches_failed", err.Error())
		return
	}
	sort.Strings(branches)

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: map[string]interface{}{
			"branches":       branches,
			"default_branch": repo.DefaultBranch,
			"current_branch": current,
		},
	})
}

// repoBranchList returns live remote branch names when a remote exists.
// Callers must surface err instead of falling back to the default branch.
func repoBranchList(ctx context.Context, h *Handler, repo *models.Repo) (branches []string, current string, err error) {
	if repo == nil {
		return nil, "", fmt.Errorf("repo is required")
	}
	switch repo.SourceType {
	case models.SourceTypeSSH:
		if repo.RemoteNodeID == nil {
			return nil, "", fmt.Errorf("ssh repo has no remote node")
		}
		if h == nil || h.Store == nil {
			return nil, "", fmt.Errorf("handler not initialized")
		}
		node, err := h.Store.GetRemoteNodeByID(ctx, *repo.RemoteNodeID)
		if err != nil {
			return nil, "", fmt.Errorf("remote node not found")
		}
		info, err := (remote.Service{Store: h.Store}).GitInfo(ctx, node, repo.SourcePath)
		if err != nil {
			return nil, "", fmt.Errorf("failed to list remote branches: %w", err)
		}
		return info.Branches, info.CurrentBranch, nil
	case models.SourceTypeGitHub:
		owner, name, err := scantarget.ParseGitHubSource(repo.SourcePath)
		if err != nil {
			return nil, "", err
		}
		token, err := githubTokenForRepo(ctx, h, repo)
		if err != nil {
			return nil, "", err
		}
		branches, err = listRemoteBranches("https://github.com/"+owner+"/"+name+".git", token)
		if err != nil {
			return nil, "", fmt.Errorf("failed to list GitHub branches: %w", err)
		}
		return branches, repo.DefaultBranch, nil
	case models.SourceTypeGitLab, models.SourceTypeGit:
		url := strings.TrimSpace(repo.SourcePath)
		if url == "" {
			return nil, "", fmt.Errorf("git source path is empty")
		}
		token := ""
		if isGitHubRemoteURL(url) {
			t, terr := githubTokenForRepo(ctx, h, repo)
			if terr != nil {
				return nil, "", terr
			}
			token = t
		}
		branches, err = listRemoteBranches(url, token)
		if err != nil {
			return nil, "", fmt.Errorf("failed to list remote branches: %w", err)
		}
		return branches, repo.DefaultBranch, nil
	default:
		origin := gitpkg.OriginURL(repo.SourcePath)
		if origin != "" {
			token := ""
			if isGitHubRemoteURL(origin) {
				t, terr := githubTokenForRepo(ctx, h, repo)
				if terr != nil {
					return nil, "", terr
				}
				token = t
			}
			branches, err = listRemoteBranches(origin, token)
			if err != nil {
				return nil, "", fmt.Errorf("failed to list remote branches: %w", err)
			}
			current, _ = gitpkg.CurrentBranch(repo.SourcePath)
			return branches, current, nil
		}
		branches, err = gitpkg.ListBranches(repo.SourcePath)
		if err != nil {
			return nil, "", fmt.Errorf("failed to list branches: %w", err)
		}
		current, _ = gitpkg.CurrentBranch(repo.SourcePath)
		return branches, current, nil
	}
}

func isGitHubRemoteURL(raw string) bool {
	return strings.Contains(strings.ToLower(raw), "github.com")
}

func githubTokenForRepo(ctx context.Context, h *Handler, repo *models.Repo) (string, error) {
	if h == nil || h.Store == nil || repo == nil || repo.UserID == "" {
		return "", nil
	}
	if strings.TrimSpace(repo.CredentialSecretID) != "" {
		secret, err := h.Store.GetSecretByID(ctx, repo.CredentialSecretID)
		if err != nil || secret.UserID != repo.UserID {
			return "", errSelectedGitHubTokenNotFound()
		}
		if secret.KeyType != models.KeyTypeGitHubToken {
			return "", errSelectedGitHubTokenNotFound()
		}
		token, err := secrets.Decrypt(secret.EncryptedValue)
		if err != nil {
			return "", errSelectedGitHubTokenNotFound()
		}
		return token, nil
	}
	list, err := h.Store.ListSecretsByUser(ctx, repo.UserID)
	if err != nil {
		return "", err
	}
	for _, secret := range list {
		if secret.KeyType != models.KeyTypeGitHubToken {
			continue
		}
		token, err := secrets.Decrypt(secret.EncryptedValue)
		if err != nil {
			return "", err
		}
		return token, nil
	}
	return "", nil
}

func errSelectedGitHubTokenNotFound() error {
	return &repoValidationError{message: "selected GitHub token was not found"}
}

type repoValidationError struct {
	message string
}

func (e *repoValidationError) Error() string {
	return e.message
}

// GetRepoFixable handles GET /api/repos/{id}/fixable — the writability
// preflight for the autonomous fix engine. It returns the derived
// {writable, reason} verdict so the UI can show a fixable indicator and disable
// the Fix action (with a reason) for repos wolf can't write a branch to. This
// is a read-only probe; it does not require autofix_enabled (the *execute* path
// is what's gated). read:repos scope.
func GetRepoFixable(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	id := chi.URLParam(r, "id")
	repo, ok := loadRepoForCaller(w, r, h.Store, id, claims)
	if !ok {
		return
	}

	res := writability.Check(r.Context(), repo, h.Store, writability.DefaultProbes(h.Store))
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: models.RepoFixable{Writable: res.Writable, CanPush: res.CanPush, Reason: res.Reason},
	})
}

// runDetection runs language/framework detection on a repo and caches the results.
// It is designed to be called in a goroutine so it doesn't block the HTTP response.
func runDetection(store db.Store, repoID, sourcePath string) {
	applyRepoDetection(context.Background(), store, repoID, sourcePath)
}

func applyRepoDetection(ctx context.Context, store db.Store, repoID, sourcePath string) {
	result, err := detector.Detect(sourcePath)
	if err != nil {
		log.Printf("detection failed for repo %s: %v", repoID, err)
		return
	}

	// Serialize languages map to JSON.
	langs := make(map[string]int, len(result.Languages))
	for lang, count := range result.Languages {
		langs[string(lang)] = count
	}
	langsJSON, _ := json.Marshal(langs)
	fwJSON, _ := json.Marshal(result.Frameworks)

	if err := store.UpdateRepoDetection(ctx, repoID, string(langsJSON), string(fwJSON)); err != nil {
		log.Printf("failed to save detection for repo %s: %v", repoID, err)
	}
}

// defaultCollectionName is the per-user "folder" that holds repositories not
// explicitly placed elsewhere. The folder model guarantees every repo belongs
// to exactly one collection, so none are orphaned and unreachable in the UI.
const defaultCollectionName = "Default"

// ensureDefaultCollection finds (or lazily creates) the user's Default
// collection and returns its ID. Used on repo create and during backfill.
func ensureDefaultCollection(ctx context.Context, store db.Store, userID string) (string, error) {
	cols, err := store.ListCollectionsByUser(ctx, userID)
	if err == nil {
		for i := range cols {
			if cols[i].Name == defaultCollectionName {
				return cols[i].ID, nil
			}
		}
	}
	col := &models.Collection{
		ID:          uuid.New().String(),
		UserID:      userID,
		Name:        defaultCollectionName,
		Description: "Repositories not assigned to another collection.",
		ScanConfig:  "{}",
	}
	if err := store.CreateCollection(ctx, col); err != nil {
		return "", err
	}
	return col.ID, nil
}

// BackfillRepoCollections assigns every repo that is in no collection to its
// owner's Default collection, enforcing the folder-model invariant for data
// created before the model existed. Idempotent: run safely at every startup.
func BackfillRepoCollections(ctx context.Context, store db.Store) error {
	cols, err := store.ListAllCollections(ctx)
	if err != nil {
		return err
	}
	member := make(map[string]bool)
	for i := range cols {
		repos, err := store.ListReposInCollection(ctx, cols[i].ID)
		if err != nil {
			continue
		}
		for j := range repos {
			member[repos[j].ID] = true
		}
	}
	repos, err := store.ListAllRepos(ctx)
	if err != nil {
		return err
	}
	for i := range repos {
		if member[repos[i].ID] {
			continue
		}
		defID, err := ensureDefaultCollection(ctx, store, repos[i].UserID)
		if err != nil {
			continue
		}
		_ = store.SetRepoCollection(ctx, repos[i].ID, defID)
	}
	return nil
}
