package routes

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/secrets"
)

var errInvalidAllowedHost = errors.New("invalid allowed host")

type createCredentialRequest struct {
	Type         models.KeyType `json:"type"`
	Name         string         `json:"name"`
	Secret       string         `json:"secret"`
	Username     string         `json:"username,omitempty"`
	KnownHosts   string         `json:"known_hosts,omitempty"`
	AllowedHosts []string       `json:"allowed_hosts"`
}

type credentialResponse struct {
	ID           string                 `json:"id"`
	Type         models.KeyType         `json:"type"`
	Name         string                 `json:"name"`
	AllowedHosts []string               `json:"allowed_hosts"`
	Masked       string                 `json:"masked"`
	Metadata     map[string]interface{} `json:"metadata"`
	CreatedAt    time.Time              `json:"created_at"`
	UpdatedAt    time.Time              `json:"updated_at"`
}

func ListCredentials(w http.ResponseWriter, r *http.Request) {
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
	stored, err := h.Store.ListSecretsByUser(r.Context(), claims.UserID)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list credentials")
		return
	}
	result := make([]credentialResponse, 0, len(stored))
	for i := range stored {
		if !isSourceCredentialType(stored[i].KeyType) {
			continue
		}
		result = append(result, presentCredential(&stored[i]))
	}
	response.WriteJSON(w, http.StatusOK, response.ListResponse{
		Data: result,
		Meta: response.ListMeta{Total: len(result), Page: 1, PerPage: len(result)},
	})
}

func CreateCredential(w http.ResponseWriter, r *http.Request) {
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
	var req createCredentialRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || req.Secret == "" || !isSourceCredentialType(req.Type) {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "type, name, and secret are required; type must be git_https, ssh_private_key, ssh_password, github_token, or gitlab_token")
		return
	}
	hosts, err := normalizeAllowedHosts(req.AllowedHosts)
	if err != nil || len(hosts) == 0 {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "allowed_hosts must contain valid DNS hostnames")
		return
	}
	if req.Type == models.KeyTypeGitHTTPS && strings.TrimSpace(req.Username) == "" {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "username is required for git_https credentials")
		return
	}
	if req.Type == models.KeyTypeSSHPrivate && strings.TrimSpace(req.KnownHosts) == "" {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "known_hosts is required for SSH private-key credentials")
		return
	}
	encrypted, err := secrets.Encrypt(req.Secret)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to encrypt credential")
		return
	}
	allowedJSON, _ := json.Marshal(hosts)
	metadata := map[string]interface{}{
		"username":        strings.TrimSpace(req.Username),
		"has_known_hosts": strings.TrimSpace(req.KnownHosts) != "",
	}
	if req.KnownHosts != "" {
		// known_hosts is host identity material, not a private credential, but it
		// is kept in DB metadata rather than echoed in API responses.
		metadata["known_hosts"] = strings.TrimSpace(req.KnownHosts)
	}
	metadataJSON, _ := json.Marshal(metadata)
	credential := &models.Secret{
		ID:             uuid.NewString(),
		UserID:         claims.UserID,
		KeyType:        req.Type,
		KeyName:        req.Name,
		EncryptedValue: encrypted,
		AllowedHosts:   string(allowedJSON),
		MetadataJSON:   string(metadataJSON),
	}
	if err := h.Store.CreateSecret(r.Context(), credential); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to store credential")
		return
	}
	response.WriteJSON(w, http.StatusCreated, response.SuccessResponse{Data: presentCredential(credential)})
}

func GetCredential(w http.ResponseWriter, r *http.Request) {
	credential, ok := loadCredential(w, r)
	if !ok {
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: presentCredential(credential)})
}

func DeleteCredential(w http.ResponseWriter, r *http.Request) {
	credential, ok := loadCredential(w, r)
	if !ok {
		return
	}
	if err := DefaultHandler.Store.DeleteSecret(r.Context(), credential.ID); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to delete credential")
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]string{"message": "credential deleted"}})
}

func loadCredential(w http.ResponseWriter, r *http.Request) (*models.Secret, bool) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil || DefaultHandler == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return nil, false
	}
	credential, err := DefaultHandler.Store.GetSecretByID(r.Context(), chi.URLParam(r, "id"))
	if err != nil || credential.UserID != claims.UserID || !isSourceCredentialType(credential.KeyType) {
		response.WriteError(w, http.StatusNotFound, "not_found", "credential not found")
		return nil, false
	}
	return credential, true
}

func presentCredential(credential *models.Secret) credentialResponse {
	var hosts []string
	_ = json.Unmarshal([]byte(credential.AllowedHosts), &hosts)
	var storedMetadata map[string]interface{}
	_ = json.Unmarshal([]byte(credential.MetadataJSON), &storedMetadata)
	metadata := map[string]interface{}{}
	if username, ok := storedMetadata["username"].(string); ok && username != "" {
		metadata["username"] = username
	}
	if hasKnownHosts, ok := storedMetadata["has_known_hosts"].(bool); ok {
		metadata["has_known_hosts"] = hasKnownHosts
	}
	return credentialResponse{
		ID: credential.ID, Type: credential.KeyType, Name: credential.KeyName,
		AllowedHosts: hosts, Masked: maskValue(""), Metadata: metadata,
		CreatedAt: credential.CreatedAt, UpdatedAt: credential.UpdatedAt,
	}
}

func isSourceCredentialType(keyType models.KeyType) bool {
	switch keyType {
	case models.KeyTypeGitHTTPS, models.KeyTypeSSHPrivate, models.KeyTypeSSHPassword,
		models.KeyTypeGitHubToken, models.KeyTypeGitLabToken:
		return true
	default:
		return false
	}
}

func normalizeAllowedHosts(input []string) ([]string, error) {
	seen := make(map[string]bool)
	hosts := make([]string, 0, len(input))
	for _, raw := range input {
		host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(raw), "."))
		if parsedIP := net.ParseIP(strings.Trim(host, "[]")); parsedIP != nil && !strings.HasPrefix(host, "*.") {
			host = parsedIP.String()
		} else if !validCredentialHostname(host) {
			return nil, errInvalidAllowedHost
		}
		if !seen[host] {
			seen[host] = true
			hosts = append(hosts, host)
		}
	}
	sort.Strings(hosts)
	return hosts, nil
}

func validCredentialHostname(host string) bool {
	if host == "" || len(host) > 253 || strings.ContainsAny(host, "/:@[] \t\r\n") {
		return false
	}
	if strings.HasPrefix(host, "*.") {
		host = strings.TrimPrefix(host, "*.")
		if host == "" || strings.Contains(host, "*") {
			return false
		}
	} else if strings.Contains(host, "*") {
		return false
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character >= 'a' && character <= 'z') ||
				(character >= '0' && character <= '9') || character == '-' {
				continue
			}
			return false
		}
	}
	return true
}
