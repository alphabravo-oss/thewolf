package routes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/api/sse"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/fix/engine"
	"github.com/alphabravocompany/thewolf/internal/loop/controller"
	"github.com/alphabravocompany/thewolf/internal/loop/tracker"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/scan/runner"
)

// activeLoops tracks running loop controllers by loop ID.
var (
	activeLoopsMu   sync.RWMutex
	activeLoops     = make(map[string]*controller.Controller)
	activeLoopCtxs  = make(map[string]context.CancelFunc)
)

// createLoopRequest is the JSON body for POST /api/loops.
type createLoopRequest struct {
	RepoID         string `json:"repo_id"`
	MaxIterations  int    `json:"max_iterations,omitempty"`
	SeverityFilter string `json:"severity_filter,omitempty"`
	RescanStrategy string `json:"rescan_strategy,omitempty"`
	Engine         string `json:"engine,omitempty"`
}

// ListLoops handles GET /api/loops — list loops for the current user.
func ListLoops(w http.ResponseWriter, r *http.Request) {
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

	loops, err := h.Store.ListLoopsByUser(r.Context(), claims.UserID)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list loops")
		return
	}

	page, perPage := parsePagination(r)
	total := len(loops)
	start, end := paginateSlice(total, page, perPage)
	paged := loops[start:end]

	response.WriteJSON(w, http.StatusOK, response.ListResponse{
		Data: paged,
		Meta: response.ListMeta{Total: total, Page: page, PerPage: perPage},
	})
}

// CreateLoop handles POST /api/loops — create and start a new loop.
func CreateLoop(w http.ResponseWriter, r *http.Request) {
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

	var req createLoopRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	if req.RepoID == "" {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "repo_id is required")
		return
	}

	// Verify the repo exists and belongs to the user.
	repo, err := h.Store.GetRepoByID(r.Context(), req.RepoID)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "repo not found")
		return
	}


	// Defaults.
	maxIter := req.MaxIterations
	if maxIter <= 0 {
		maxIter = 5
	}
	rescan := models.RescanStrategy(req.RescanStrategy)
	if rescan == "" {
		rescan = models.RescanFull
	}
	engineName := req.Engine
	if engineName == "" {
		engineName = "auto"
	}
	now := time.Now()
	loop := &models.Loop{
		ID:             uuid.New().String(),
		UserID:         claims.UserID,
		RepoID:         req.RepoID,
		Status:         models.LoopStatusRunning,
		MaxIterations:  maxIter,
		SeverityFilter: req.SeverityFilter,
		RescanStrategy: rescan,
		StartedAt:      &now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if err := h.Store.CreateLoop(r.Context(), loop); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to create loop")
		return
	}

	// Parse severity filter.
	var severities []models.Severity
	if req.SeverityFilter != "" {
		for _, s := range splitLoopCSV(req.SeverityFilter) {
			severities = append(severities, models.Severity(s))
		}
	}

	// Initialize fix engine.
	eng, err := engine.NewEngine(engineName)
	if err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", fmt.Sprintf("invalid engine: %s", engineName))
		return
	}

	// Build controller config.
	scanCfg := runner.RunConfig{
		RepoPath:    repo.SourcePath,
		Registry:    plugin.Global,
		Concurrency: 0, // auto
		Timeout:     10 * time.Minute,
	}

	cfg := controller.Config{
		RepoPath:       repo.SourcePath,
		MaxIterations:  maxIter,
		Severities:     severities,
		RescanStrategy: rescan,
		ScanConfig:     scanCfg,
		FixEngine:      eng,
		FixTimeout:     5 * time.Minute,
		OnIterationDone: func(iteration int, diff *tracker.IterationDiff, warnings []string) {
			// Publish SSE event.
			if SSEBroker != nil {
				data := fmt.Sprintf(
					`{"loop_id":"%s","iteration":%d,"fixed":%d,"new":%d,"remaining":%d}`,
					loop.ID, iteration, diff.FixedCount, diff.NewCount, diff.RemainingCount,
				)
				SSEBroker.Publish("loop:"+loop.ID, sse.Event{
					Type: "loop_iteration",
					Data: data,
				})
			}

			// Update loop in database.
			loop.CurrentIteration = iteration
			loop.TotalFindingsFixed += diff.FixedCount
			loop.TotalFindingsNew += diff.NewCount
			loop.TotalFindingsRemaining = diff.RemainingCount + diff.NewCount
			loop.UpdatedAt = time.Now()
			_ = h.Store.UpdateLoop(context.Background(), loop)
		},
	}

	ctrl := controller.New(cfg)

	// Track the controller.
	ctx, cancel := context.WithCancel(context.Background())
	activeLoopsMu.Lock()
	activeLoops[loop.ID] = ctrl
	activeLoopCtxs[loop.ID] = cancel
	activeLoopsMu.Unlock()

	// Run loop in background goroutine.
	go func() {
		defer func() {
			activeLoopsMu.Lock()
			delete(activeLoops, loop.ID)
			delete(activeLoopCtxs, loop.ID)
			activeLoopsMu.Unlock()
			cancel()
		}()

		result, err := ctrl.Run(ctx)
		if err != nil {
			loop.Status = models.LoopStatusFailed
		} else {
			loop.Status = result.Status
			loop.TotalFindingsInitial = result.TotalFindingsInitial
			loop.TotalFindingsFixed = result.TotalFindingsFixed
			loop.TotalFindingsNew = result.TotalFindingsNew
			loop.TotalFindingsRemaining = result.TotalFindingsRemaining
			loop.GuardrailWarnings = result.GuardrailWarnings
			loop.CurrentIteration = result.CurrentIteration
		}

		completedAt := time.Now()
		loop.CompletedAt = &completedAt
		loop.UpdatedAt = completedAt
		_ = h.Store.UpdateLoop(context.Background(), loop)

		// Publish completion SSE event.
		if SSEBroker != nil {
			data := fmt.Sprintf(
				`{"loop_id":"%s","status":"%s","iterations":%d,"fixed":%d,"remaining":%d}`,
				loop.ID, loop.Status, loop.CurrentIteration,
				loop.TotalFindingsFixed, loop.TotalFindingsRemaining,
			)
			SSEBroker.Publish("loop:"+loop.ID, sse.Event{
				Type: "loop_completed",
				Data: data,
			})
		}
	}()

	response.WriteJSON(w, http.StatusCreated, response.SuccessResponse{Data: loop})
}

// GetLoop handles GET /api/loops/:id — get loop details.
func GetLoop(w http.ResponseWriter, r *http.Request) {
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

	id := chi.URLParam(r, "id")
	loop, err := h.Store.GetLoopByID(r.Context(), id)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("loop %s not found", id))
		return
	}


	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: loop})
}

// StreamLoop handles GET /api/loops/:id/stream — SSE endpoint for loop progress.
func StreamLoop(w http.ResponseWriter, r *http.Request) {
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

	loopID := chi.URLParam(r, "id")

	// Verify loop exists and belongs to user.
	_, err := h.Store.GetLoopByID(r.Context(), loopID)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("loop %s not found", loopID))
		return
	}


	broker := SSEBroker
	if broker == nil {
		// Fallback: poll-based SSE.
		streamLoopPoll(w, r, h, loopID)
		return
	}

	topic := "loop:" + loopID
	clientID := uuid.New().String()
	client := broker.Subscribe(topic, clientID)
	defer broker.Unsubscribe(topic, clientID)

	sse.ServeHTTP(w, r, client)
}

// streamLoopPoll is a fallback SSE implementation that polls the database.
func streamLoopPoll(w http.ResponseWriter, r *http.Request, h *Handler, loopID string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Send initial status.
	loop, err := h.Store.GetLoopByID(r.Context(), loopID)
	if err != nil {
		return
	}
	sendSSE(w, flusher, "loop_status", fmt.Sprintf(
		`{"loop_id":"%s","status":"%s","iteration":%d,"fixed":%d,"remaining":%d}`,
		loop.ID, loop.Status, loop.CurrentIteration,
		loop.TotalFindingsFixed, loop.TotalFindingsRemaining,
	))

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			loop, err = h.Store.GetLoopByID(r.Context(), loopID)
			if err != nil {
				return
			}
			sendSSE(w, flusher, "loop_status", fmt.Sprintf(
				`{"loop_id":"%s","status":"%s","iteration":%d,"fixed":%d,"remaining":%d}`,
				loop.ID, loop.Status, loop.CurrentIteration,
				loop.TotalFindingsFixed, loop.TotalFindingsRemaining,
			))
			if loop.Status == models.LoopStatusCompleted ||
				loop.Status == models.LoopStatusFailed ||
				loop.Status == models.LoopStatusStopped {
				sendSSE(w, flusher, "loop_completed", fmt.Sprintf(
					`{"loop_id":"%s","status":"%s"}`, loop.ID, loop.Status,
				))
				return
			}
		}
	}
}

// PauseLoop handles POST /api/loops/:id/pause — pause a running loop.
func PauseLoop(w http.ResponseWriter, r *http.Request) {
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

	id := chi.URLParam(r, "id")
	loop, err := h.Store.GetLoopByID(r.Context(), id)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("loop %s not found", id))
		return
	}


	if loop.Status != models.LoopStatusRunning {
		response.WriteError(w, http.StatusConflict, "conflict", "loop is not running")
		return
	}

	activeLoopsMu.RLock()
	ctrl, ok := activeLoops[id]
	activeLoopsMu.RUnlock()

	if !ok {
		response.WriteError(w, http.StatusConflict, "conflict", "loop controller not found")
		return
	}

	ctrl.Pause()
	loop.Status = models.LoopStatusPaused
	loop.UpdatedAt = time.Now()
	_ = h.Store.UpdateLoop(r.Context(), loop)

	if SSEBroker != nil {
		SSEBroker.Publish("loop:"+id, sse.Event{
			Type: "loop_paused",
			Data: fmt.Sprintf(`{"loop_id":"%s","status":"paused"}`, id),
		})
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: loop})
}

// ResumeLoop handles POST /api/loops/:id/resume — resume a paused loop.
func ResumeLoop(w http.ResponseWriter, r *http.Request) {
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

	id := chi.URLParam(r, "id")
	loop, err := h.Store.GetLoopByID(r.Context(), id)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("loop %s not found", id))
		return
	}


	if loop.Status != models.LoopStatusPaused {
		response.WriteError(w, http.StatusConflict, "conflict", "loop is not paused")
		return
	}

	activeLoopsMu.RLock()
	ctrl, ok := activeLoops[id]
	activeLoopsMu.RUnlock()

	if !ok {
		response.WriteError(w, http.StatusConflict, "conflict", "loop controller not found")
		return
	}

	ctrl.Resume()
	loop.Status = models.LoopStatusRunning
	loop.UpdatedAt = time.Now()
	_ = h.Store.UpdateLoop(r.Context(), loop)

	if SSEBroker != nil {
		SSEBroker.Publish("loop:"+id, sse.Event{
			Type: "loop_resumed",
			Data: fmt.Sprintf(`{"loop_id":"%s","status":"running"}`, id),
		})
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: loop})
}

// StopLoop handles POST /api/loops/:id/stop — stop a running or paused loop.
func StopLoop(w http.ResponseWriter, r *http.Request) {
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

	id := chi.URLParam(r, "id")
	loop, err := h.Store.GetLoopByID(r.Context(), id)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("loop %s not found", id))
		return
	}


	if loop.Status != models.LoopStatusRunning && loop.Status != models.LoopStatusPaused {
		response.WriteError(w, http.StatusConflict, "conflict", "loop is not running or paused")
		return
	}

	activeLoopsMu.RLock()
	ctrl, ok := activeLoops[id]
	cancelFn, hasCancel := activeLoopCtxs[id]
	activeLoopsMu.RUnlock()

	if ok {
		ctrl.Stop()
	}
	if hasCancel {
		cancelFn()
	}

	now := time.Now()
	loop.Status = models.LoopStatusStopped
	loop.CompletedAt = &now
	loop.UpdatedAt = now
	_ = h.Store.UpdateLoop(r.Context(), loop)

	if SSEBroker != nil {
		SSEBroker.Publish("loop:"+id, sse.Event{
			Type: "loop_stopped",
			Data: fmt.Sprintf(`{"loop_id":"%s","status":"stopped"}`, id),
		})
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: loop})
}

// splitLoopCSV splits a comma-separated string into trimmed non-empty parts.
func splitLoopCSV(s string) []string {
	var parts []string
	for _, p := range splitByComma(s) {
		p = trimSpace(p)
		if p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}

// splitByComma is a minimal comma split to avoid importing strings in a second location.
func splitByComma(s string) []string {
	var result []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			result = append(result, s[start:i])
			start = i + 1
		}
	}
	result = append(result, s[start:])
	return result
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && s[start] == ' ' {
		start++
	}
	for end > start && s[end-1] == ' ' {
		end--
	}
	return s[start:end]
}
