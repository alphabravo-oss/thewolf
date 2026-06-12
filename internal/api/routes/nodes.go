package routes

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/remote"
)

type remoteNodeRequest struct {
	Name               string  `json:"name"`
	Host               string  `json:"host"`
	Port               int     `json:"port"`
	Username           string  `json:"username"`
	AuthType           string  `json:"auth_type"`
	CredentialSecretID *string `json:"credential_secret_id"`
	KnownHosts         string  `json:"known_hosts"`
	BasePath           string  `json:"base_path"`
	Enabled            *bool   `json:"enabled"`
}

func ListRemoteNodes(w http.ResponseWriter, r *http.Request) {
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
	nodes, err := h.Store.ListRemoteNodesByUser(r.Context(), claims.UserID)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list remote nodes")
		return
	}
	response.WriteJSON(w, http.StatusOK, response.ListResponse{
		Data: nodes,
		Meta: response.ListMeta{Total: len(nodes), Page: 1, PerPage: len(nodes)},
	})
}

func CreateRemoteNode(w http.ResponseWriter, r *http.Request) {
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
	var req remoteNodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Host) == "" || strings.TrimSpace(req.Username) == "" {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "name, host, and username are required")
		return
	}
	if req.Port == 0 {
		req.Port = 22
	}
	if req.Port < 1 || req.Port > 65535 {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "port must be between 1 and 65535")
		return
	}
	if req.AuthType == "" {
		req.AuthType = "private_key"
	}
	if !validSSHAuthType(req.AuthType) {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "auth_type must be private_key or password")
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	now := time.Now().UTC()
	node := &models.RemoteNode{
		ID:                 uuid.New().String(),
		UserID:             claims.UserID,
		Name:               strings.TrimSpace(req.Name),
		Host:               strings.TrimSpace(req.Host),
		Port:               req.Port,
		Username:           strings.TrimSpace(req.Username),
		AuthType:           req.AuthType,
		CredentialSecretID: req.CredentialSecretID,
		KnownHosts:         strings.TrimSpace(req.KnownHosts),
		BasePath:           strings.TrimSpace(req.BasePath),
		Enabled:            enabled,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := h.Store.CreateRemoteNode(r.Context(), node); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to create remote node")
		return
	}
	response.WriteJSON(w, http.StatusCreated, response.SuccessResponse{Data: node})
}

func GetRemoteNode(w http.ResponseWriter, r *http.Request) {
	node, ok := loadRemoteNode(w, r)
	if !ok {
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: node})
}

func UpdateRemoteNode(w http.ResponseWriter, r *http.Request) {
	node, ok := loadRemoteNode(w, r)
	if !ok {
		return
	}
	h := DefaultHandler
	var req remoteNodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if strings.TrimSpace(req.Name) != "" {
		node.Name = strings.TrimSpace(req.Name)
	}
	if strings.TrimSpace(req.Host) != "" {
		node.Host = strings.TrimSpace(req.Host)
	}
	if req.Port != 0 {
		if req.Port < 1 || req.Port > 65535 {
			response.WriteError(w, http.StatusBadRequest, "validation_error", "port must be between 1 and 65535")
			return
		}
		node.Port = req.Port
	}
	if strings.TrimSpace(req.Username) != "" {
		node.Username = strings.TrimSpace(req.Username)
	}
	if req.AuthType != "" {
		if !validSSHAuthType(req.AuthType) {
			response.WriteError(w, http.StatusBadRequest, "validation_error", "auth_type must be private_key or password")
			return
		}
		node.AuthType = req.AuthType
	}
	if req.CredentialSecretID != nil {
		node.CredentialSecretID = req.CredentialSecretID
	}
	if req.KnownHosts != "" {
		node.KnownHosts = strings.TrimSpace(req.KnownHosts)
	}
	if req.BasePath != "" {
		node.BasePath = strings.TrimSpace(req.BasePath)
	}
	if req.Enabled != nil {
		node.Enabled = *req.Enabled
	}
	if err := h.Store.UpdateRemoteNode(r.Context(), node); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to update remote node")
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: node})
}

func DeleteRemoteNode(w http.ResponseWriter, r *http.Request) {
	node, ok := loadRemoteNode(w, r)
	if !ok {
		return
	}
	h := DefaultHandler
	repos, _ := h.Store.ListReposByUser(r.Context(), node.UserID)
	for _, repo := range repos {
		if repo.RemoteNodeID != nil && *repo.RemoteNodeID == node.ID {
			response.WriteError(w, http.StatusConflict, "node_in_use", "remote node is referenced by one or more repos")
			return
		}
	}
	if err := h.Store.DeleteRemoteNode(r.Context(), node.ID); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to delete remote node")
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]string{"message": "remote node deleted"}})
}

func CheckRemoteNode(w http.ResponseWriter, r *http.Request) {
	node, ok := loadRemoteNode(w, r)
	if !ok {
		return
	}
	h := DefaultHandler
	svc := remote.Service{Store: h.Store}
	err := svc.Check(r.Context(), node)
	status := "ok"
	errMsg := ""
	if err != nil {
		status = "failed"
		errMsg = err.Error()
	}
	_ = h.Store.TouchRemoteNodeCheck(r.Context(), node.ID, status, errMsg)
	if err != nil {
		response.WriteError(w, http.StatusBadGateway, "ssh_check_failed", errMsg)
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]string{"status": status}})
}

func BrowseRemoteNode(w http.ResponseWriter, r *http.Request) {
	node, ok := loadRemoteNode(w, r)
	if !ok {
		return
	}
	h := DefaultHandler
	result, err := (remote.Service{Store: h.Store}).Browse(r.Context(), node, r.URL.Query().Get("path"))
	if err != nil {
		response.WriteError(w, http.StatusBadGateway, "ssh_browse_failed", err.Error())
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: result})
}

func RemoteGitInfo(w http.ResponseWriter, r *http.Request) {
	node, ok := loadRemoteNode(w, r)
	if !ok {
		return
	}
	p := strings.TrimSpace(r.URL.Query().Get("path"))
	if p == "" {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "path is required")
		return
	}
	h := DefaultHandler
	info, err := (remote.Service{Store: h.Store}).GitInfo(r.Context(), node, p)
	if err != nil {
		response.WriteError(w, http.StatusBadGateway, "ssh_git_info_failed", err.Error())
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: info})
}

func loadRemoteNode(w http.ResponseWriter, r *http.Request) (*models.RemoteNode, bool) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return nil, false
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return nil, false
	}
	node, err := h.Store.GetRemoteNodeByID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "remote node not found")
		return nil, false
	}
	return node, true
}

func validSSHAuthType(v string) bool {
	return v == "private_key" || v == "password"
}
