package routes

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/remote"
	"github.com/alphabravocompany/thewolf/internal/sshclient"
)

// SSHRunnerOverride lets tests inject a fake SSH runner. When nil, the
// production sshclient.Client is used.
var SSHRunnerOverride sshclient.Runner

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
	if req.CredentialSecretID != nil {
		if !credentialSecretAllowed(w, r, h, claims.UserID, req.AuthType, *req.CredentialSecretID) {
			return
		}
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
		claims := auth.GetUserFromContext(r.Context())
		if claims == nil {
			response.WriteError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
			return
		}
		if !credentialSecretAllowed(w, r, h, claims.UserID, node.AuthType, *req.CredentialSecretID) {
			return
		}
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
	svc := remote.Service{Store: h.Store, Runner: SSHRunnerOverride}
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
	result, err := (remote.Service{Store: h.Store, Runner: SSHRunnerOverride}).Browse(r.Context(), node, r.URL.Query().Get("path"))
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
	info, err := (remote.Service{Store: h.Store, Runner: SSHRunnerOverride}).GitInfo(r.Context(), node, p)
	if err != nil {
		response.WriteError(w, http.StatusBadGateway, "ssh_git_info_failed", err.Error())
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: info})
}

type discoverReposRequest struct {
	BasePath string `json:"base_path"`
}

// DiscoverNodeRepos walks a remote node looking for .git directories under
// base_path (or node.BasePath when omitted) and returns repo metadata.
func DiscoverNodeRepos(w http.ResponseWriter, r *http.Request) {
	node, ok := loadRemoteNode(w, r)
	if !ok {
		return
	}
	var req discoverReposRequest
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
			return
		}
	}
	base := strings.TrimSpace(req.BasePath)
	if base == "" {
		base = node.BasePath
	}
	if !remotePathWithinBase(base, node.BasePath) {
		response.WriteError(w, http.StatusForbidden, "forbidden", "base_path must stay under the remote node base_path")
		return
	}
	h := DefaultHandler
	repos, err := (remote.Service{Store: h.Store, Runner: SSHRunnerOverride}).DiscoverRepos(r.Context(), node, base)
	if err != nil {
		response.WriteError(w, http.StatusBadGateway, "ssh_discover_failed", err.Error())
		return
	}
	// Ensure consistent stable ordering and non-nil array in JSON.
	sort.Slice(repos, func(i, j int) bool { return repos[i].Path < repos[j].Path })
	if repos == nil {
		repos = []remote.DiscoveredRepo{}
	}
	response.WriteJSON(w, http.StatusOK, response.ListResponse{
		Data: repos,
		Meta: response.ListMeta{Total: len(repos), Page: 1, PerPage: len(repos)},
	})
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
	// Ownership: a user can only operate on the nodes they created; admins on
	// any. 404 (not 403) so node IDs can't be enumerated.
	if !canModifyOwned(claims, node.UserID) {
		response.WriteError(w, http.StatusNotFound, "not_found", "remote node not found")
		return nil, false
	}
	return node, true
}

func validSSHAuthType(v string) bool {
	return v == "private_key" || v == "password"
}

func credentialSecretAllowed(w http.ResponseWriter, r *http.Request, h *Handler, userID, authType, secretID string) bool {
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
	switch authType {
	case "", "private_key":
		if secret.KeyType != models.KeyTypeSSHPrivate {
			response.WriteError(w, http.StatusBadRequest, "validation_error", "private_key nodes require an ssh_private_key secret")
			return false
		}
	case "password":
		if secret.KeyType != models.KeyTypeSSHPassword {
			response.WriteError(w, http.StatusBadRequest, "validation_error", "password nodes require an ssh_password secret")
			return false
		}
	}
	return true
}

func remotePathWithinBase(pathValue, baseValue string) bool {
	baseValue = normalizeRemotePath(baseValue)
	if baseValue == "" {
		return true
	}
	pathValue = normalizeRemotePath(pathValue)
	if pathValue == "" {
		pathValue = baseValue
	}
	return pathValue == baseValue || strings.HasPrefix(pathValue+"/", baseValue+"/")
}

func normalizeRemotePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" || p == "/" {
		return p
	}
	for strings.Contains(p, "//") {
		p = strings.ReplaceAll(p, "//", "/")
	}
	return strings.TrimRight(p, "/")
}
