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
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/secrets"
	"github.com/alphabravocompany/thewolf/internal/setup"
)

type createSecretRequest struct {
	KeyType models.KeyType `json:"key_type"`
	KeyName string         `json:"key_name"`
	Value   string         `json:"value"`
}

type maskedSecret struct {
	ID        string         `json:"id"`
	KeyType   models.KeyType `json:"key_type"`
	KeyName   string         `json:"key_name"`
	Value     string         `json:"value"`
	CreatedAt time.Time      `json:"created_at"`
}

func maskValue(_ string) string {
	return "********"
}

func maskedStoredSecret(_ models.Secret) string {
	// Never derive presentation metadata from secret material. In particular,
	// do not expose the value's length or suffix and do not honor legacy
	// persisted masks that contained those details.
	return maskValue("")
}

func ListSecrets(w http.ResponseWriter, r *http.Request) {
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

	secs, err := h.Store.ListSecretsByUser(r.Context(), claims.UserID)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list secrets")
		return
	}

	masked := make([]maskedSecret, len(secs))
	for i, s := range secs {
		masked[i] = maskedSecret{
			ID:        s.ID,
			KeyType:   s.KeyType,
			KeyName:   s.KeyName,
			Value:     maskedStoredSecret(s),
			CreatedAt: s.CreatedAt,
		}
	}

	response.WriteJSON(w, http.StatusOK, response.ListResponse{
		Data: masked,
		Meta: response.ListMeta{Total: len(masked), Page: 1, PerPage: len(masked)},
	})
}

func CreateSecret(w http.ResponseWriter, r *http.Request) {
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

	var req createSecretRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	if req.KeyName == "" || req.Value == "" {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "key_name and value are required")
		return
	}
	if req.KeyType == "" {
		req.KeyType = models.KeyTypeCustom
	}

	encrypted, err := secrets.Encrypt(req.Value)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to encrypt secret")
		return
	}

	now := time.Now()
	secret := &models.Secret{
		ID:             uuid.New().String(),
		UserID:         claims.UserID,
		KeyType:        req.KeyType,
		KeyName:        req.KeyName,
		EncryptedValue: encrypted,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if err := h.Store.CreateSecret(r.Context(), secret); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to store secret")
		return
	}

	response.WriteJSON(w, http.StatusCreated, response.SuccessResponse{
		Data: maskedSecret{
			ID:        secret.ID,
			KeyType:   secret.KeyType,
			KeyName:   secret.KeyName,
			Value:     maskValue(req.Value),
			CreatedAt: secret.CreatedAt,
		},
	})
}

func DeleteSecret(w http.ResponseWriter, r *http.Request) {
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
	secret, err := h.Store.GetSecretByID(r.Context(), id)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "secret not found")
		return
	}
	if !canModifyOwned(claims, secret.UserID) {
		// 404 (not 403) so a user can't probe which secret IDs exist.
		response.WriteError(w, http.StatusNotFound, "not_found", "secret not found")
		return
	}

	if err := h.Store.DeleteSecret(r.Context(), id); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to delete secret")
		return
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]string{"message": "secret deleted"}})
}

func ListPlugins(w http.ResponseWriter, r *http.Request) {
	h := DefaultHandler
	reg := plugin.Global
	if h != nil && h.Registry != nil {
		reg = h.Registry
	}

	plugins := reg.GetAll()
	toolStatus := setup.AllToolStatus()

	// Build a lookup of setup tool info by name.
	toolMap := make(map[string]setup.ToolStatus, len(toolStatus))
	for _, ts := range toolStatus {
		toolMap[ts.Name] = ts
	}

	type pluginInfo struct {
		Name           string                `json:"name"`
		Description    string                `json:"description,omitempty"`
		ProjectURL     string                `json:"project_url,omitempty"`
		Category       models.Category       `json:"category"`
		Languages      []models.Language     `json:"languages"`
		Available      bool                  `json:"available"`
		Version        string                `json:"version,omitempty"`
		Installable    bool                  `json:"installable"`
		InstallVia     string                `json:"install_via,omitempty"`
		InstallMethods []setup.InstallMethod `json:"install_methods,omitempty"`
	}

	result := make([]pluginInfo, len(plugins))
	for i, p := range plugins {
		info := pluginInfo{
			Name:      p.Name(),
			Category:  p.Category(),
			Languages: p.Languages(),
			Available: p.CheckAvailable(),
		}
		// Merge install metadata from setup package
		if ts, ok := toolMap[p.Name()]; ok {
			info.Description = ts.Description
			info.ProjectURL = ts.ProjectURL
			info.Version = ts.Version
			info.Installable = ts.Installable
			info.InstallVia = ts.InstallVia
			info.InstallMethods = ts.InstallMethods
		}
		result[i] = info
	}

	response.WriteJSON(w, http.StatusOK, response.ListResponse{
		Data: result,
		Meta: response.ListMeta{Total: len(result), Page: 1, PerPage: len(result)},
	})
}

// InstallPlugin installs a tool via SSE, streaming output to the client.
func InstallPlugin(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	// Validate tool exists
	if _, ok := setup.GetTool(name); !ok {
		response.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("unknown tool %q", name))
		return
	}

	// Set up SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "streaming not supported")
		return
	}

	// SSE writer that sends each line as an event
	sseWriter := &sseWriter{w: w, flusher: flusher}

	ver, err := setup.InstallTool(name, sseWriter)
	if err != nil {
		fmt.Fprintf(sseWriter, "\nERROR: %v\n", err)
		sendSSE(w, flusher, "error", fmt.Sprintf(`{"error":"%s"}`, err.Error()))
		return
	}

	sendSSE(w, flusher, "done", fmt.Sprintf(`{"name":"%s","version":"%s","installed":true}`, name, ver))
}

// sseWriter wraps an http.ResponseWriter to send each Write as an SSE data event.
type sseWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

func (s *sseWriter) Write(p []byte) (int, error) {
	lines := strings.Split(string(p), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		sendSSE(s.w, s.flusher, "log", line)
	}
	return len(p), nil
}

// SetupStatus returns the platform info, prerequisites, and tool statuses.
func SetupStatus(w http.ResponseWriter, r *http.Request) {
	type statusResponse struct {
		Platform Platform           `json:"platform"`
		Prereqs  setup.Prereqs      `json:"prereqs"`
		Tools    []setup.ToolStatus `json:"tools"`
	}

	plat := setup.DetectPlatform()
	prereqs := setup.DetectPrereqs()
	tools := setup.AllToolStatus()

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: statusResponse{
			Platform: Platform{OS: plat.OS, Arch: plat.Arch, Distro: plat.Distro},
			Prereqs:  prereqs,
			Tools:    tools,
		},
	})
}

// Platform is a simplified platform struct for API responses.
type Platform struct {
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	Distro string `json:"distro,omitempty"`
}
