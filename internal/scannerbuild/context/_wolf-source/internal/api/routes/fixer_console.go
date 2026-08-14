package routes

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/fix/console"
	"github.com/alphabravocompany/thewolf/internal/fix/install"
	"github.com/alphabravocompany/thewolf/internal/models"
)

const (
	fixerConsoleShellSetting = "fixer_console_shell"
	maxConsoleStdinBytes     = 4096
)

type createFixerConsoleRequest struct {
	Kind   string `json:"kind"`
	Engine string `json:"engine"`
}

type fixerConsoleInputRequest struct {
	Data string `json:"data"`
}

// CreateFixerConsole handles POST /api/fixes/consoles — enqueue a login or
// (when enabled) operator shell on the fixer worker. Admin-only: OAuth files
// live in the worker HOME, not per-user storage.
func CreateFixerConsole(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return
	}
	if !claims.IsAdmin() {
		response.WriteError(w, http.StatusForbidden, "forbidden", "fixer console is an administrator action")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	var req createFixerConsoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	if kind == "" {
		kind = models.FixerConsoleLogin
	}
	engine := strings.ToLower(strings.TrimSpace(req.Engine))
	switch kind {
	case models.FixerConsoleLogin:
		if _, err := console.LoginArgs(engine); err != nil {
			response.WriteError(w, http.StatusBadRequest, "validation_error", err.Error())
			return
		}
	case models.FixerConsoleShell:
		if !fixerConsoleShellEnabled(r, h) {
			response.WriteError(w, http.StatusForbidden, "console_shell_disabled",
				"operator shell is off; set fixer_console_shell=true in settings")
			return
		}
		engine = ""
	case models.FixerConsoleInstall:
		if !install.Supported(engine) {
			response.WriteError(w, http.StatusBadRequest, "validation_error",
				"can install claude, codex, or opencode")
			return
		}
	default:
		response.WriteError(w, http.StatusBadRequest, "validation_error", "kind must be login, install, or shell")
		return
	}

	now := time.Now().UTC()
	cons := &models.FixerConsole{
		ID:        uuid.New().String(),
		UserID:    claims.UserID,
		Kind:      kind,
		Engine:    engine,
		Status:    models.FixerConsoleQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := h.Store.EnqueueFixerConsole(r.Context(), cons); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to enqueue console")
		return
	}
	response.WriteJSON(w, http.StatusCreated, response.SuccessResponse{Data: cons})
}

// GetFixerConsole handles GET /api/fixes/consoles/{id}.
func GetFixerConsole(w http.ResponseWriter, r *http.Request) {
	cons, ok := loadOwnedConsole(w, r)
	if !ok {
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: cons})
}

// StreamFixerConsole handles GET /api/fixes/consoles/{id}/stream — tails the
// worker transcript over SSE and includes last_url when the CLI prints one.
func StreamFixerConsole(w http.ResponseWriter, r *http.Request) {
	cons, ok := loadOwnedConsole(w, r)
	if !ok {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	logPath := ""
	if fs := fixArtifacts(); fs != nil {
		logPath = fs.ConsoleLogPath(cons.ID)
	}

	sendSSE(w, flusher, "console_status", consoleStatusJSON(cons))
	var offset int64
	offset = relayConsoleData(w, flusher, logPath, offset)

	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()
	h := DefaultHandler
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			offset = relayConsoleData(w, flusher, logPath, offset)
			latest, err := h.Store.GetFixerConsoleByID(r.Context(), cons.ID)
			if err != nil || latest == nil {
				return
			}
			sendSSE(w, flusher, "console_status", consoleStatusJSON(latest))
			if !models.FixerConsoleActive(latest.Status) {
				_ = relayConsoleData(w, flusher, logPath, offset)
				sendSSE(w, flusher, "console_completed", consoleStatusJSON(latest))
				return
			}
		}
	}
}

// InputFixerConsole handles POST /api/fixes/consoles/{id}/input — queues
// keystrokes for the worker to write to the process stdin.
func InputFixerConsole(w http.ResponseWriter, r *http.Request) {
	cons, ok := loadOwnedConsole(w, r)
	if !ok {
		return
	}
	if !models.FixerConsoleActive(cons.Status) {
		response.WriteError(w, http.StatusConflict, "conflict", "console is not running")
		return
	}
	var req fixerConsoleInputRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	if req.Data == "" {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "data required")
		return
	}
	if len(req.Data) > maxConsoleStdinBytes {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "data exceeds 4096 bytes")
		return
	}
	if err := DefaultHandler.Store.AppendFixerConsoleStdin(r.Context(), cons.ID, req.Data); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to queue input")
		return
	}
	response.WriteJSON(w, http.StatusAccepted, response.SuccessResponse{Data: map[string]string{"status": "queued"}})
}

// CancelFixerConsole handles DELETE /api/fixes/consoles/{id}.
func CancelFixerConsole(w http.ResponseWriter, r *http.Request) {
	cons, ok := loadOwnedConsole(w, r)
	if !ok {
		return
	}
	if !models.FixerConsoleActive(cons.Status) {
		response.WriteError(w, http.StatusConflict, "conflict", "console is already finished")
		return
	}
	now := time.Now().UTC()
	cons.Status = models.FixerConsoleCancelled
	cons.FinishedAt = &now
	if err := DefaultHandler.Store.UpdateFixerConsole(r.Context(), cons); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to cancel console")
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: cons})
}

func loadOwnedConsole(w http.ResponseWriter, r *http.Request) (*models.FixerConsole, bool) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return nil, false
	}
	if !claims.IsAdmin() {
		response.WriteError(w, http.StatusForbidden, "forbidden", "fixer console is an administrator action")
		return nil, false
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return nil, false
	}
	id := chi.URLParam(r, "id")
	cons, err := h.Store.GetFixerConsoleByID(r.Context(), id)
	if err != nil || cons == nil || !ownsFixerConsole(cons, claims) {
		response.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("console %s not found", id))
		return nil, false
	}
	return cons, true
}

func ownsFixerConsole(cons *models.FixerConsole, claims *auth.Claims) bool {
	return cons.UserID == "" || cons.UserID == claims.UserID
}

func fixerConsoleShellEnabled(r *http.Request, h *Handler) bool {
	if h == nil || h.Store == nil {
		return false
	}
	v, err := h.Store.GetSetting(r.Context(), fixerConsoleShellSetting)
	return err == nil && strings.EqualFold(strings.TrimSpace(v), "true")
}

func consoleStatusJSON(cons *models.FixerConsole) string {
	payload, _ := json.Marshal(map[string]any{
		"type":     "console_status",
		"id":       cons.ID,
		"status":   cons.Status,
		"kind":     cons.Kind,
		"engine":   cons.Engine,
		"last_url": cons.LastURL,
		"error":    cons.Error,
	})
	return string(payload)
}
