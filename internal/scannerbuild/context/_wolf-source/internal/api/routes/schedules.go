package routes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/wolflog"
)

var allowedScheduleIntervals = map[int]bool{15: true, 60: true, 360: true, 1440: true}

type scheduleRequest struct {
	RepoID          *string `json:"repo_id"`
	CollectionID    *string `json:"collection_id"`
	IntervalMinutes *int    `json:"interval_minutes"`
	Branch          *string `json:"branch"`
	Profile         *string `json:"profile"`
	QuietStart      *string `json:"quiet_start"`
	QuietEnd        *string `json:"quiet_end"`
	Enabled         *bool   `json:"enabled"`
}

func ListSchedules(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}
	rows, err := h.Store.ListScanSchedulesByUser(r.Context(), claims.UserID)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list schedules")
		return
	}
	if rows == nil {
		rows = []models.ScanSchedule{}
	}
	response.WriteJSON(w, http.StatusOK, response.ListResponse{
		Data: rows,
		Meta: response.ListMeta{Total: len(rows), Page: 1, PerPage: len(rows)},
	})
}

func CreateSchedule(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}
	var req scheduleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	s := &models.ScanSchedule{
		ID:      uuid.NewString(),
		UserID:  claims.UserID,
		Enabled: true,
		Profile: "standard",
	}
	if err := applyScheduleRequest(s, req, true); err != nil {
		response.WriteError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	if !authorizeScheduleTarget(w, r, h, claims, s) {
		return
	}
	if err := h.Store.CreateScanSchedule(r.Context(), s); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to create schedule")
		return
	}
	response.WriteJSON(w, http.StatusCreated, response.SuccessResponse{Data: s})
}

func UpdateSchedule(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}
	s := loadScheduleForOwner(w, r, h, claims)
	if s == nil {
		return
	}
	var req scheduleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	if err := applyScheduleRequest(s, req, false); err != nil {
		response.WriteError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	if !authorizeScheduleTarget(w, r, h, claims, s) {
		return
	}
	if err := h.Store.UpdateScanSchedule(r.Context(), s); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to update schedule")
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: s})
}

func DeleteSchedule(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}
	s := loadScheduleForOwner(w, r, h, claims)
	if s == nil {
		return
	}
	if err := h.Store.DeleteScanSchedule(r.Context(), s.ID); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to delete schedule")
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]string{"message": "schedule deleted"}})
}

func loadScheduleForOwner(w http.ResponseWriter, r *http.Request, h *Handler, claims *auth.Claims) *models.ScanSchedule {
	id := chi.URLParam(r, "id")
	s, err := h.Store.GetScanScheduleByID(r.Context(), id)
	if err != nil || s == nil || s.UserID != claims.UserID {
		response.WriteError(w, http.StatusNotFound, "not_found", "schedule not found")
		return nil
	}
	return s
}

func applyScheduleRequest(s *models.ScanSchedule, req scheduleRequest, create bool) error {
	if create {
		hasRepo := req.RepoID != nil && strings.TrimSpace(*req.RepoID) != ""
		hasCol := req.CollectionID != nil && strings.TrimSpace(*req.CollectionID) != ""
		if hasRepo == hasCol {
			return fmt.Errorf("exactly one of repo_id or collection_id is required")
		}
		if req.IntervalMinutes == nil {
			return fmt.Errorf("interval_minutes is required")
		}
	}
	if req.RepoID != nil || req.CollectionID != nil {
		repoID, colID := s.RepoID, s.CollectionID
		if req.RepoID != nil {
			repoID = strings.TrimSpace(*req.RepoID)
		}
		if req.CollectionID != nil {
			colID = strings.TrimSpace(*req.CollectionID)
		}
		if (repoID == "") == (colID == "") {
			return fmt.Errorf("exactly one of repo_id or collection_id is required")
		}
		s.RepoID, s.CollectionID = repoID, colID
	}
	if req.IntervalMinutes != nil {
		if !allowedScheduleIntervals[*req.IntervalMinutes] {
			return fmt.Errorf("interval_minutes must be 15, 60, 360, or 1440")
		}
		s.IntervalMinutes = *req.IntervalMinutes
	}
	if req.Branch != nil {
		s.Branch = strings.TrimSpace(*req.Branch)
	}
	if req.Profile != nil {
		s.Profile = strings.TrimSpace(*req.Profile)
	}
	if s.Profile == "" {
		s.Profile = "standard"
	}
	s.Profile = strings.ToLower(s.Profile)
	switch s.Profile {
	case "standard", "fast", "pr", "release", "full":
	default:
		return fmt.Errorf("profile must be standard, fast, pr, release, or full")
	}
	if req.QuietStart != nil {
		s.QuietStart = strings.TrimSpace(*req.QuietStart)
	}
	if req.QuietEnd != nil {
		s.QuietEnd = strings.TrimSpace(*req.QuietEnd)
	}
	if s.QuietStart != "" {
		if _, ok := parseHHMM(s.QuietStart); !ok {
			return fmt.Errorf("quiet_start must be HH:MM")
		}
	}
	if s.QuietEnd != "" {
		if _, ok := parseHHMM(s.QuietEnd); !ok {
			return fmt.Errorf("quiet_end must be HH:MM")
		}
	}
	if req.Enabled != nil {
		s.Enabled = *req.Enabled
	}
	return nil
}

func authorizeScheduleTarget(w http.ResponseWriter, r *http.Request, h *Handler, claims *auth.Claims, s *models.ScanSchedule) bool {
	if s.RepoID != "" {
		_, ok := loadRepoForCaller(w, r, h.Store, s.RepoID, claims)
		return ok
	}
	col, err := h.Store.GetCollectionByID(r.Context(), s.CollectionID)
	if err != nil || col == nil || col.UserID != claims.UserID {
		response.WriteError(w, http.StatusNotFound, "not_found", "collection not found")
		return false
	}
	return true
}

func parseHHMM(s string) (int, bool) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		return 0, false
	}
	return t.Hour()*60 + t.Minute(), true
}

func inQuietHours(now time.Time, start, end string) bool {
	if strings.TrimSpace(start) == "" || strings.TrimSpace(end) == "" {
		return false
	}
	s, ok1 := parseHHMM(start)
	e, ok2 := parseHHMM(end)
	if !ok1 || !ok2 || s == e {
		return false
	}
	t := now.Hour()*60 + now.Minute()
	if s < e {
		return t >= s && t < e
	}
	return t >= s || t < e
}

func scheduleDue(s models.ScanSchedule, now time.Time) bool {
	if !s.Enabled {
		return false
	}
	if inQuietHours(now, s.QuietStart, s.QuietEnd) {
		return false
	}
	if s.LastRunAt == nil {
		return true
	}
	return now.Sub(*s.LastRunAt) >= time.Duration(s.IntervalMinutes)*time.Minute
}

func skipUnchangedSHA(repo *models.Repo, completedSHA, lastSHA string) bool {
	if completedSHA == "" {
		return false
	}
	if repo != nil && repo.LastCommitSHA != "" {
		return completedSHA == repo.LastCommitSHA
	}
	return lastSHA != "" && completedSHA == lastSHA
}

func scanPendingOrRunning(ctx context.Context, h *Handler, repoID, branch string) bool {
	scans, err := h.Store.ListScansByRepo(ctx, repoID)
	if err != nil {
		return false
	}
	for i := range scans {
		if scans[i].Branch != branch {
			continue
		}
		switch scans[i].Status {
		case models.ScanStatusPending, models.ScanStatusRunning:
			return true
		}
	}
	return false
}

func latestCompletedSHA(ctx context.Context, h *Handler, repoID, branch string) string {
	scans, err := h.Store.ListScansByRepo(ctx, repoID)
	if err != nil {
		return ""
	}
	for i := range scans {
		if scans[i].Branch == branch && scans[i].Status == models.ScanStatusCompleted && scans[i].CommitSHA != "" {
			return scans[i].CommitSHA
		}
	}
	return ""
}

func scheduleRepos(ctx context.Context, h *Handler, s *models.ScanSchedule) ([]models.Repo, error) {
	if s.RepoID != "" {
		repo, err := h.Store.GetRepoByID(ctx, s.RepoID)
		if err != nil {
			return nil, err
		}
		return []models.Repo{*repo}, nil
	}
	return h.Store.ListReposInCollection(ctx, s.CollectionID)
}

func RunDueSchedules(ctx context.Context, h *Handler) {
	if h == nil || h.Store == nil {
		return
	}
	schedules, err := h.Store.ListEnabledScanSchedules(ctx)
	if err != nil {
		wolflog.Warn().Err(err).Msg("list enabled schedules failed")
		return
	}
	now := time.Now()
	for i := range schedules {
		s := &schedules[i]
		if ctx.Err() != nil {
			return
		}
		if !scheduleDue(*s, now) {
			continue
		}
		repos, err := scheduleRepos(ctx, h, s)
		if err != nil {
			wolflog.Warn().Err(err).Str("schedule_id", s.ID).Msg("resolve schedule repos failed")
			continue
		}
		lastSHA := s.LastSHA
		for j := range repos {
			repo := &repos[j]
			branch := s.Branch
			if branch == "" {
				branch = repo.DefaultBranch
			}
			if scanPendingOrRunning(ctx, h, repo.ID, branch) {
				continue
			}
			completedSHA := latestCompletedSHA(ctx, h, repo.ID, branch)
			if skipUnchangedSHA(repo, completedSHA, s.LastSHA) {
				if completedSHA != "" {
					lastSHA = completedSHA
				}
				continue
			}
			profile := s.Profile
			if profile == "" {
				profile = "standard"
			}
			req := createScanRequest{RepoID: repo.ID, Branch: branch, Profile: profile}
			if s.CollectionID != "" {
				cid := s.CollectionID
				req.CollectionID = &cid
			}
			if _, err := enqueueScan(ctx, h, s.UserID, repo, req); err != nil {
				wolflog.Warn().Err(err).Str("schedule_id", s.ID).Str("repo_id", repo.ID).Msg("schedule enqueue failed")
				continue
			}
			if repo.LastCommitSHA != "" {
				lastSHA = repo.LastCommitSHA
			}
		}
		runAt := now.UTC()
		s.LastRunAt = &runAt
		s.LastSHA = lastSHA
		if err := h.Store.UpdateScanSchedule(ctx, s); err != nil {
			wolflog.Warn().Err(err).Str("schedule_id", s.ID).Msg("update schedule last_run failed")
		}
	}
}

// StartScheduleLoop polls due schedules.
// ponytail: minute poll; upgrade next_run_at index.
func StartScheduleLoop(ctx context.Context, h *Handler) {
	if h == nil {
		return
	}
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	RunDueSchedules(ctx, h)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			RunDueSchedules(ctx, h)
		}
	}
}
