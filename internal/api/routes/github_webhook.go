package routes

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/scantarget"
	"github.com/alphabravocompany/thewolf/internal/wolflog"
)

func GitHubWebhook(w http.ResponseWriter, r *http.Request) {
	h := DefaultHandler
	if h == nil || h.Store == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "failed to read body")
		return
	}
	secret, _ := h.Store.GetSetting(r.Context(), "github_webhook_secret")
	if !validGitHubSignature(secret, r.Header.Get("X-Hub-Signature-256"), body) {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid webhook signature")
		return
	}
	switch r.Header.Get("X-GitHub-Event") {
	case "push":
		handleGitHubPush(r.Context(), h, body)
	case "pull_request":
		handleGitHubPullRequest(r.Context(), h, body)
	}
	response.WriteJSON(w, http.StatusAccepted, map[string]any{"accepted": true})
}

func validGitHubSignature(secret, header string, body []byte) bool {
	if strings.TrimSpace(secret) == "" || !strings.HasPrefix(header, "sha256=") {
		return false
	}
	want, err := hex.DecodeString(strings.TrimPrefix(header, "sha256="))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return hmac.Equal(mac.Sum(nil), want)
}

func handleGitHubPush(ctx context.Context, h *Handler, body []byte) {
	var payload struct {
		Ref        string `json:"ref"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return
	}
	branch, ok := branchFromRef(payload.Ref)
	if !ok {
		return
	}
	repo := findGitHubRepo(ctx, h, payload.Repository.FullName)
	if repo == nil {
		return
	}
	if scanPendingOrRunning(ctx, h, repo.ID, branch) {
		return
	}
	profile := pushProfile(ctx, h, repo.ID)
	_, err := enqueueScan(ctx, h, repo.UserID, repo, createScanRequest{RepoID: repo.ID, Branch: branch, Profile: profile})
	if err != nil {
		wolflog.Warn().Err(err).Str("repo_id", repo.ID).Msg("github push enqueue failed")
	}
}

func handleGitHubPullRequest(ctx context.Context, h *Handler, body []byte) {
	var payload struct {
		Action      string `json:"action"`
		PullRequest struct {
			Head struct {
				Ref  string `json:"ref"`
				Repo struct {
					FullName string `json:"full_name"`
					Fork     bool   `json:"fork"`
					CloneURL string `json:"clone_url"`
				} `json:"repo"`
			} `json:"head"`
			Base struct {
				Repo struct {
					FullName string `json:"full_name"`
				} `json:"repo"`
			} `json:"base"`
		} `json:"pull_request"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return
	}
	switch payload.Action {
	case "opened", "synchronize", "reopened":
	default:
		return
	}
	repo := findGitHubRepo(ctx, h, payload.Repository.FullName)
	if repo == nil {
		return
	}
	branch := strings.TrimSpace(payload.PullRequest.Head.Ref)
	if branch == "" {
		return
	}
	untrusted := payload.PullRequest.Head.Repo.Fork ||
		!strings.EqualFold(payload.PullRequest.Head.Repo.FullName, payload.PullRequest.Base.Repo.FullName)
	if untrusted {
		source := &scanSourceRequest{
			Kind: "git",
			URL:  payload.PullRequest.Head.Repo.CloneURL,
			Ref:  branch,
		}
		sourceRepo, err := materializeScanSource(ctx, h, repo.UserID, source)
		if err != nil {
			wolflog.Warn().Err(err).Str("repo_id", repo.ID).Msg("untrusted fork source skipped")
			return
		}
		if scanPendingOrRunning(ctx, h, sourceRepo.ID, branch) {
			return
		}
		_, err = enqueueScan(ctx, h, repo.UserID, sourceRepo, createScanRequest{
			RepoID: sourceRepo.ID, Branch: branch, Profile: "fast", Source: source,
		})
		if err != nil {
			wolflog.Warn().Err(err).Str("repo_id", repo.ID).Msg("github untrusted pr enqueue failed")
		}
		return
	}
	if scanPendingOrRunning(ctx, h, repo.ID, branch) {
		return
	}
	_, err := enqueueScan(ctx, h, repo.UserID, repo, createScanRequest{RepoID: repo.ID, Branch: branch, Profile: "fast"})
	if err != nil {
		wolflog.Warn().Err(err).Str("repo_id", repo.ID).Msg("github pr enqueue failed")
	}
}

func branchFromRef(ref string) (string, bool) {
	const prefix = "refs/heads/"
	if !strings.HasPrefix(ref, prefix) {
		return "", false
	}
	branch := strings.TrimPrefix(ref, prefix)
	return branch, branch != ""
}

// ponytail: linear ListAllRepos match; upgrade github owner/name index.
func findGitHubRepo(ctx context.Context, h *Handler, fullName string) *models.Repo {
	owner, name, err := scantarget.ParseGitHubSource(fullName)
	if err != nil {
		return nil
	}
	repos, err := h.Store.ListAllRepos(ctx)
	if err != nil {
		return nil
	}
	for i := range repos {
		if repos[i].SourceType != models.SourceTypeGitHub {
			continue
		}
		o, n, perr := scantarget.ParseGitHubSource(repos[i].SourcePath)
		if perr != nil {
			continue
		}
		if strings.EqualFold(o, owner) && strings.EqualFold(n, name) {
			return &repos[i]
		}
	}
	return nil
}

func pushProfile(ctx context.Context, h *Handler, repoID string) string {
	schedules, err := h.Store.ListEnabledScanSchedules(ctx)
	if err != nil {
		return "standard"
	}
	for i := range schedules {
		if schedules[i].RepoID == repoID && schedules[i].Profile != "" {
			return schedules[i].Profile
		}
	}
	return "standard"
}
