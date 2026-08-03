package routes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/scantarget"
)

type scanSourceRequest struct {
	Kind         string `json:"kind"`
	Name         string `json:"name"`
	URL          string `json:"url,omitempty"`
	Ref          string `json:"ref,omitempty"`
	CredentialID string `json:"credential_id,omitempty"`

	NodeID     string `json:"node_id,omitempty"`
	Path       string `json:"path,omitempty"`
	Host       string `json:"host,omitempty"`
	Port       int    `json:"port,omitempty"`
	Username   string `json:"username,omitempty"`
	BasePath   string `json:"base_path,omitempty"`
	KnownHosts string `json:"known_hosts,omitempty"`
}

func materializeScanSource(ctx context.Context, h *Handler, userID string, source *scanSourceRequest) (*models.Repo, error) {
	if source == nil {
		return nil, fmt.Errorf("source is required")
	}
	switch strings.ToLower(strings.TrimSpace(source.Kind)) {
	case "git":
		return materializeGitSource(ctx, h, userID, source)
	case "ssh":
		return materializeSSHSource(ctx, h, userID, source)
	default:
		return nil, fmt.Errorf("source.kind must be git or ssh")
	}
}

func materializeGitSource(ctx context.Context, h *Handler, userID string, source *scanSourceRequest) (*models.Repo, error) {
	canonical, host, err := scantarget.CanonicalGitURL(source.URL)
	if err != nil {
		return nil, err
	}
	if source.CredentialID != "" {
		allowedTypes := []models.KeyType{
			models.KeyTypeGitHTTPS, models.KeyTypeGitHubToken, models.KeyTypeGitLabToken,
		}
		if strings.HasPrefix(canonical, "ssh://") {
			allowedTypes = []models.KeyType{models.KeyTypeSSHPrivate}
		}
		if err := validateSourceCredential(ctx, h, userID, source.CredentialID, host, allowedTypes...); err != nil {
			return nil, err
		}
	}
	fingerprint := sourceFingerprint(userID, "git", canonical)
	if existing := findSourceRepo(ctx, h, userID, fingerprint); existing != nil {
		if source.CredentialID != "" && existing.CredentialSecretID != source.CredentialID {
			existing.CredentialSecretID = source.CredentialID
			_ = h.Store.UpdateRepo(ctx, existing)
		}
		return existing, nil
	}
	name := strings.TrimSpace(source.Name)
	if name == "" {
		name = strings.TrimSuffix(path.Base(strings.TrimSuffix(canonical, "/")), ".git")
	}
	ref := strings.TrimSpace(source.Ref)
	if ref == "" {
		ref = "HEAD"
	}
	repo := &models.Repo{
		ID: uuid.NewString(), UserID: userID, Name: name,
		SourceType: models.SourceTypeGit, SourcePath: canonical,
		SourceFingerprint: fingerprint, CredentialSecretID: source.CredentialID,
		DefaultBranch: ref,
	}
	return createVisibleSourceRepo(ctx, h, repo)
}

func materializeSSHSource(ctx context.Context, h *Handler, userID string, source *scanSourceRequest) (*models.Repo, error) {
	remotePath := path.Clean(strings.TrimSpace(source.Path))
	if remotePath == "." || !path.IsAbs(remotePath) {
		return nil, fmt.Errorf("source.path must be an absolute remote path")
	}
	var node *models.RemoteNode
	var oneShotNode bool
	var fingerprint string
	if strings.TrimSpace(source.NodeID) != "" {
		loaded, err := h.Store.GetRemoteNodeByID(ctx, strings.TrimSpace(source.NodeID))
		if err != nil || loaded.UserID != userID {
			return nil, fmt.Errorf("SSH node not found")
		}
		node = loaded
		fingerprint = sourceFingerprint(userID, "ssh", node.ID, remotePath)
	} else {
		host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(source.Host), "."))
		if host == "" || strings.TrimSpace(source.Username) == "" || strings.TrimSpace(source.KnownHosts) == "" {
			return nil, fmt.Errorf("one-shot SSH source requires host, username, path, known_hosts, and credential_id")
		}
		if err := scantarget.ValidateRemoteDestination(ctx, host); err != nil {
			return nil, err
		}
		if source.CredentialID == "" {
			return nil, fmt.Errorf("one-shot SSH source requires credential_id")
		}
		credential, err := validateSourceCredentialValue(ctx, h, userID, source.CredentialID, host,
			models.KeyTypeSSHPrivate, models.KeyTypeSSHPassword)
		if err != nil {
			return nil, err
		}
		basePath := path.Clean(strings.TrimSpace(source.BasePath))
		if basePath == "." || basePath == "" {
			basePath = path.Dir(remotePath)
		}
		if !pathWithinBase(remotePath, basePath) {
			return nil, fmt.Errorf("source.path must be inside source.base_path")
		}
		port := source.Port
		if port == 0 {
			port = 22
		}
		if port < 1 || port > 65535 {
			return nil, fmt.Errorf("source.port must be between 1 and 65535")
		}
		// A one-shot source is identified by its connection coordinates, not
		// by the generated node UUID. This makes retries and concurrent API
		// submissions converge on the same visible repository.
		fingerprint = sourceFingerprint(
			userID, "ssh", host, fmt.Sprintf("%d", port),
			strings.TrimSpace(source.Username), basePath, remotePath,
		)
		authType := "private_key"
		if credential.KeyType == models.KeyTypeSSHPassword {
			authType = "password"
		}
		if existing := findSourceRepo(ctx, h, userID, fingerprint); existing != nil {
			// Credential rotation on an otherwise identical one-shot source
			// updates the durable node rather than silently retaining stale
			// authentication material.
			if existing.RemoteNodeID != nil {
				if existingNode, loadErr := h.Store.GetRemoteNodeByID(ctx, *existing.RemoteNodeID); loadErr == nil &&
					existingNode.UserID == userID {
					credentialID := credential.ID
					existingNode.AuthType = authType
					existingNode.CredentialSecretID = &credentialID
					existingNode.KnownHosts = strings.TrimSpace(source.KnownHosts)
					existingNode.Enabled = true
					if updateErr := h.Store.UpdateRemoteNode(ctx, existingNode); updateErr != nil {
						return nil, fmt.Errorf("update SSH source credential: %w", updateErr)
					}
				}
			}
			return existing, nil
		}
		credentialID := credential.ID
		node = &models.RemoteNode{
			ID: uuid.NewString(), UserID: userID, Name: sourceName(source, host),
			Host: host, Port: port, Username: strings.TrimSpace(source.Username),
			AuthType: authType, CredentialSecretID: &credentialID,
			KnownHosts: strings.TrimSpace(source.KnownHosts), BasePath: basePath, Enabled: true,
		}
		if err := h.Store.CreateRemoteNode(ctx, node); err != nil {
			return nil, fmt.Errorf("create SSH source node: %w", err)
		}
		oneShotNode = true
	}
	if node.BasePath != "" && !pathWithinBase(remotePath, path.Clean(node.BasePath)) {
		if oneShotNode {
			_ = h.Store.DeleteRemoteNode(ctx, node.ID)
		}
		return nil, fmt.Errorf("source.path is outside the SSH node base_path")
	}
	if existing := findSourceRepo(ctx, h, userID, fingerprint); existing != nil {
		if oneShotNode {
			_ = h.Store.DeleteRemoteNode(ctx, node.ID)
		}
		return existing, nil
	}
	nodeID := node.ID
	repo := &models.Repo{
		ID: uuid.NewString(), UserID: userID, Name: sourceName(source, path.Base(remotePath)),
		SourceType: models.SourceTypeSSH, SourcePath: remotePath, RemotePath: remotePath,
		RemoteNodeID: &nodeID, SourceFingerprint: fingerprint,
		DefaultBranch: defaultString(strings.TrimSpace(source.Ref), "HEAD"),
	}
	created, err := createVisibleSourceRepo(ctx, h, repo)
	if err != nil {
		if oneShotNode {
			_ = h.Store.DeleteRemoteNode(ctx, node.ID)
		}
		return nil, err
	}
	if oneShotNode && created.ID != repo.ID {
		// Another request won the unique source-fingerprint race.
		_ = h.Store.DeleteRemoteNode(ctx, node.ID)
	}
	return created, nil
}

func createVisibleSourceRepo(ctx context.Context, h *Handler, repo *models.Repo) (*models.Repo, error) {
	if err := h.Store.CreateRepo(ctx, repo); err != nil {
		if existing := findSourceRepo(ctx, h, repo.UserID, repo.SourceFingerprint); existing != nil {
			return existing, nil
		}
		return nil, fmt.Errorf("create source repository: %w", err)
	}
	if collectionID, err := ensureDefaultCollection(ctx, h.Store, repo.UserID); err == nil {
		_ = h.Store.SetRepoCollection(ctx, repo.ID, collectionID)
	}
	return repo, nil
}

func findSourceRepo(ctx context.Context, h *Handler, userID, fingerprint string) *models.Repo {
	repos, err := h.Store.ListReposByUser(ctx, userID)
	if err != nil {
		return nil
	}
	for i := range repos {
		if repos[i].SourceFingerprint == fingerprint {
			return &repos[i]
		}
	}
	return nil
}

func validateSourceCredential(ctx context.Context, h *Handler, userID, credentialID, host string, allowedTypes ...models.KeyType) error {
	_, err := validateSourceCredentialValue(ctx, h, userID, credentialID, host, allowedTypes...)
	return err
}

func validateSourceCredentialValue(ctx context.Context, h *Handler, userID, credentialID, host string, allowedTypes ...models.KeyType) (*models.Secret, error) {
	credential, err := h.Store.GetSecretByID(ctx, credentialID)
	if err != nil || credential.UserID != userID {
		return nil, fmt.Errorf("source credential not found")
	}
	typeAllowed := false
	for _, allowed := range allowedTypes {
		if credential.KeyType == allowed {
			typeAllowed = true
			break
		}
	}
	if !typeAllowed {
		return nil, fmt.Errorf("credential type %s is not valid for this source", credential.KeyType)
	}
	var hosts []string
	if json.Unmarshal([]byte(credential.AllowedHosts), &hosts) != nil || len(hosts) == 0 {
		return nil, fmt.Errorf("source credential has no allowed_hosts binding")
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, allowed := range hosts {
		allowed = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(allowed), "."))
		if host == allowed || (strings.HasPrefix(allowed, "*.") && strings.HasSuffix(host, strings.TrimPrefix(allowed, "*"))) {
			return credential, nil
		}
	}
	return nil, fmt.Errorf("source credential is not allowed for host %s", host)
}

func sourceFingerprint(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func pathWithinBase(candidate, base string) bool {
	candidate = path.Clean(candidate)
	base = path.Clean(base)
	return candidate == base || strings.HasPrefix(candidate, strings.TrimSuffix(base, "/")+"/")
}

func sourceName(source *scanSourceRequest, fallback string) string {
	if name := strings.TrimSpace(source.Name); name != "" {
		return name
	}
	return fallback
}

func validateScanRequestSelectors(h *Handler, req *createScanRequest) error {
	switch req.Profile {
	case "", "standard", "full":
	case "targeted":
		if len(req.Tools) == 0 && len(req.Categories) == 0 && len(req.IncludePaths) == 0 {
			return fmt.Errorf("targeted profile requires tools, categories, or include_paths")
		}
	default:
		return fmt.Errorf("profile must be standard, full, or targeted")
	}
	knownCategories := map[string]bool{
		"sast": true, "sca": true, "secrets": true, "quality": true, "container": true,
		"docs": true, "license": true, "sbom": true, "infra": true, "dast": true,
	}
	for _, category := range req.Categories {
		if !knownCategories[strings.ToLower(strings.TrimSpace(category))] {
			return fmt.Errorf("unknown scan category %q", category)
		}
	}
	for _, toolName := range append(append([]string{}, req.Tools...), req.DisabledTools...) {
		if h.Registry == nil {
			return fmt.Errorf("scanner registry is unavailable")
		}
		if _, err := h.Registry.Get(toolName); err != nil {
			return fmt.Errorf("unknown scanner tool %q", toolName)
		}
	}
	for _, pattern := range append(append([]string{}, req.IncludePaths...), req.ExcludePaths...) {
		if err := validateScopePattern(pattern); err != nil {
			return err
		}
	}
	return nil
}

func validateScopePattern(pattern string) error {
	value := strings.TrimSpace(strings.ReplaceAll(pattern, "\\", "/"))
	windowsAbsolute := len(value) >= 3 &&
		((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= 'A' && value[0] <= 'Z')) &&
		value[1] == ':' && value[2] == '/'
	if value == "" || strings.ContainsRune(value, '\x00') || path.IsAbs(value) ||
		filepath.IsAbs(value) || filepath.VolumeName(value) != "" || windowsAbsolute {
		return fmt.Errorf("scope paths must be non-empty relative patterns")
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == ".." {
			return fmt.Errorf("scope path %q contains traversal", pattern)
		}
	}
	return nil
}

func toolsForProfile(h *Handler, req createScanRequest, languages []models.Language) []string {
	if len(req.Tools) > 0 {
		return append([]string(nil), req.Tools...)
	}
	if req.Profile != "full" && len(req.Categories) == 0 {
		return nil
	}
	categorySet := make(map[models.Category]bool)
	for _, category := range req.Categories {
		categorySet[models.Category(strings.ToLower(strings.TrimSpace(category)))] = true
	}
	languageSet := make(map[models.Language]bool)
	for _, language := range languages {
		languageSet[language] = true
	}
	var result []string
	for _, scanner := range h.Registry.GetAll() {
		if scanner.Category() == models.CategoryDAST {
			continue
		}
		if len(categorySet) > 0 && !categorySet[scanner.Category()] {
			continue
		}
		applicable := len(scanner.Languages()) == 0 || len(languageSet) == 0
		for _, language := range scanner.Languages() {
			if languageSet[language] {
				applicable = true
				break
			}
		}
		if applicable {
			result = append(result, scanner.Name())
		}
	}
	sort.Strings(result)
	return result
}

func requestedScopeJSON(req createScanRequest) string {
	value, _ := json.Marshal(map[string]interface{}{
		"include_paths": req.IncludePaths,
		"exclude_paths": req.ExcludePaths,
		"profile":       req.Profile,
		"categories":    req.Categories,
	})
	return string(value)
}
