package openapi

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Version is the API version this spec describes.
const Version = "1.0.0"

// Endpoint describes one API operation. The table of all Endpoints is the
// single source of truth for both the OpenAPI spec and the route-coverage
// test that guards against undocumented routes.
type Endpoint struct {
	Method  string // GET, POST, PUT, DELETE
	Path    string // relative to /api/v1, e.g. "/repos/{id}"
	Tag     string // grouping in the UI
	Summary string
	// Scope is "" for public endpoints, "self" for authenticated endpoints
	// with no scope requirement, or a concrete scope string.
	Scope string
	// Body, when non-empty, names a request-body schema in components.
	Body string
	// List marks endpoints that return a paginated ListResponse.
	List bool
}

// Endpoints is the complete, ordered catalog of API operations.
func Endpoints() []Endpoint {
	return []Endpoint{
		// Health & meta (public).
		{"GET", "/health", "system", "Liveness probe", "", "", false},
		{"GET", "/ready", "system", "Readiness probe", "", "", false},
		{"GET", "/version", "system", "Build version info", "", "", false},

		// Auth (public).
		{"GET", "/auth/settings", "auth", "Get public authentication settings", "", "", false},
		{"POST", "/auth/register", "auth", "Register a new user", "", "RegisterRequest", false},
		{"POST", "/auth/login", "auth", "Log in and receive a JWT", "", "LoginRequest", false},

		// Auth / session (authenticated).
		{"POST", "/auth/logout", "auth", "Log out the current session", "self", "", false},
		{"GET", "/auth/me", "auth", "Get the current identity", "self", "", false},
		{"PUT", "/auth/password", "auth", "Change the current user's password", "self", "ChangePasswordRequest", false},

		// API tokens (authenticated, self-managed).
		{"GET", "/auth/tokens", "tokens", "List the caller's API tokens", "self", "", true},
		{"POST", "/auth/tokens", "tokens", "Create an API token", "self", "CreateTokenRequest", false},
		{"DELETE", "/auth/tokens/{id}", "tokens", "Revoke an API token", "self", "", false},

		// Audit log (admin).
		{"GET", "/audit-log", "audit", "List mutating-request audit entries", "admin", "", true},

		// Users (admin).
		{"GET", "/users", "users", "List users", "admin", "", true},
		{"POST", "/users", "users", "Create a user", "admin", "CreateUserRequest", false},
		{"DELETE", "/users/{id}", "users", "Delete a user", "admin", "", false},

		// Repos.
		{"GET", "/repos", "repos", "List repositories", "read:repos", "", true},
		{"POST", "/repos", "repos", "Add a repository", "write:repos", "CreateRepoRequest", false},
		{"GET", "/repos/{id}", "repos", "Get a repository", "read:repos", "", false},
		{"PUT", "/repos/{id}", "repos", "Update a repository", "write:repos", "UpdateRepoRequest", false},
		{"DELETE", "/repos/{id}", "repos", "Delete a repository", "write:repos", "", false},
		{"GET", "/repos/{id}/branches", "repos", "List a repository's branches", "read:repos", "", true},
		{"GET", "/repos/{id}/baselines", "repos", "List repository scan baselines", "read:scans", "", true},
		{"POST", "/repos/{id}/baselines", "repos", "Create a repository scan baseline", "write:scans", "CreateBaselineRequest", false},

		// Remote SSH nodes.
		{"GET", "/nodes", "nodes", "List remote SSH nodes", "read:config", "", true},
		{"POST", "/nodes", "nodes", "Create a remote SSH node", "write:config", "RemoteNodeRequest", false},
		{"GET", "/nodes/{id}", "nodes", "Get a remote SSH node", "read:config", "", false},
		{"PUT", "/nodes/{id}", "nodes", "Update a remote SSH node", "write:config", "RemoteNodeRequest", false},
		{"DELETE", "/nodes/{id}", "nodes", "Delete a remote SSH node", "write:config", "", false},
		{"POST", "/nodes/{id}/check", "nodes", "Check SSH connectivity", "write:config", "", false},
		{"GET", "/nodes/{id}/browse", "nodes", "Browse directories on a remote SSH node", "read:config", "", false},
		{"GET", "/nodes/{id}/git-info", "nodes", "Inspect a remote git working tree", "read:config", "", false},

		// Collections.
		{"GET", "/collections", "collections", "List collections", "read:repos", "", true},
		{"POST", "/collections", "collections", "Create a collection", "write:repos", "CreateCollectionRequest", false},
		{"GET", "/collections/{id}", "collections", "Get a collection", "read:repos", "", false},
		{"PUT", "/collections/{id}", "collections", "Update a collection", "write:repos", "CreateCollectionRequest", false},
		{"DELETE", "/collections/{id}", "collections", "Delete a collection", "write:repos", "", false},
		{"POST", "/collections/{id}/repos", "collections", "Add a repo to a collection", "write:repos", "CollectionRepoRequest", false},
		{"DELETE", "/collections/{id}/repos/{repoId}", "collections", "Remove a repo from a collection", "write:repos", "", false},
		{"GET", "/collections/{id}/tools", "collections", "List a collection's tools", "read:repos", "", true},
		{"GET", "/collections/{id}/metrics", "collections", "Get a collection's metrics", "read:repos", "", false},

		// Scans.
		{"GET", "/scans", "scans", "List scans", "read:scans", "", true},
		{"GET", "/scans/trends", "scans", "Scan trends over time", "read:scans", "", false},
		{"POST", "/scans", "scans", "Start a scan", "write:scans", "CreateScanRequest", false},
		{"GET", "/scans/{id}", "scans", "Get a scan", "read:scans", "", false},
		{"GET", "/scans/{id}/findings", "scans", "List a scan's findings", "read:scans", "", true},
		{"GET", "/scans/{id}/findings/stats", "scans", "Finding statistics for a scan", "read:scans", "", false},
		{"GET", "/scans/{id}/stream", "scans", "Stream scan progress (SSE)", "read:scans", "", false},
		{"GET", "/scans/{id}/report", "scans", "Get a scan's report", "read:scans", "", false},
		{"GET", "/scans/{id}/manifest", "scans", "Get a scan's manifest", "read:scans", "", false},
		{"GET", "/scans/{id}/sarif", "scans", "Get a scan's SARIF output", "read:scans", "", false},
		{"GET", "/scans/{id}/coverage", "scans", "Get a scan's coverage", "read:scans", "", false},
		{"GET", "/scans/{id}/gate", "scans", "Get a scan's quality gate result", "read:scans", "", false},
		{"GET", "/scans/{id}/diff", "scans", "Get a scan's baseline diff", "read:scans", "", false},
		{"POST", "/scans/{id}/compare", "scans", "Compare a scan to a baseline scan", "read:scans", "CompareScanRequest", false},
		{"GET", "/scans/{id}/compare/{compareId}", "scans", "Compare two scans", "read:scans", "", false},
		{"GET", "/scans/{id}/tools", "scans", "List a scan's tools", "read:scans", "", true},
		{"GET", "/scans/{id}/scanner-runs", "scans", "List scanner run records", "read:scans", "", true},
		{"GET", "/scans/{id}/tools/{toolName}/output", "scans", "Get a tool's raw output", "read:scans", "", false},
		{"GET", "/scans/{id}/artifacts/{artifactId}/download", "scans", "Download a scan artifact", "read:scans", "", false},
		{"GET", "/scans/{id}/ai-logs", "scans", "List a scan's AI logs", "read:scans", "", true},
		{"GET", "/scans/{id}/tool-summaries", "scans", "List a scan's tool summaries", "read:scans", "", true},
		{"GET", "/scans/{id}/recommendations", "scans", "List a scan's recommendations", "read:scans", "", true},
		{"DELETE", "/scans/{id}", "scans", "Cancel a scan", "write:scans", "", false},
		{"DELETE", "/scans/{id}/tools/{toolName}", "scans", "Cancel one tool of a scan", "write:scans", "", false},

		// Fleet aggregates.
		{"GET", "/fleet/posture", "fleet", "Fleet-wide posture summary", "read:scans", "", false},
		{"GET", "/fleet/inventory", "fleet", "Fleet inventory grouped by source / collection / language", "read:repos", "", false},
		{"GET", "/fleet/needs-attention", "fleet", "Top repos that need attention, scored", "read:scans", "", true},

		// Findings.
		{"GET", "/findings", "findings", "List findings", "read:findings", "", true},
		{"GET", "/findings/export", "findings", "Export findings", "read:findings", "", false},
		{"GET", "/findings/trends", "findings", "Finding trends over time", "read:findings", "", false},
		{"GET", "/findings/trends/export", "findings", "Export finding trends", "read:findings", "", false},
		{"GET", "/findings/{id}", "findings", "Get a finding", "read:findings", "", false},
		{"PUT", "/findings/{id}/status", "findings", "Change a finding's status", "write:findings", "FindingStatusRequest", false},

		// SARIF.
		{"POST", "/sarif/import", "sarif", "Import SARIF findings", "write:scans", "SARIFImportRequest", false},

		// Suppressions.
		{"GET", "/suppressions", "suppressions", "List finding suppressions", "read:findings", "", true},
		{"POST", "/suppressions", "suppressions", "Create a finding suppression", "write:findings", "SuppressionRequest", false},
		{"POST", "/suppressions/preview", "suppressions", "Preview a finding suppression", "write:findings", "SuppressionRequest", false},
		{"DELETE", "/suppressions/{id}", "suppressions", "Revoke a finding suppression", "write:findings", "", false},

		// Policies.
		{"GET", "/policies", "policies", "List quality policies", "read:config", "", true},
		{"POST", "/policies", "policies", "Create a quality policy", "write:config", "PolicyRequest", false},
		{"PUT", "/policies/{id}", "policies", "Update a quality policy", "write:config", "PolicyRequest", false},

		// Fixes.
		{"GET", "/fixes", "fixes", "List fixes", "read:fixes", "", true},
		{"POST", "/fixes", "fixes", "Start a fix", "write:fixes", "CreateFixRequest", false},
		{"GET", "/fixes/{id}", "fixes", "Get a fix", "read:fixes", "", false},
		{"GET", "/fixes/{id}/stream", "fixes", "Stream fix progress (SSE)", "read:fixes", "", false},
		{"DELETE", "/fixes/{id}", "fixes", "Cancel a fix", "write:fixes", "", false},

		// Loops.
		{"GET", "/loops", "loops", "List loops", "read:loops", "", true},
		{"POST", "/loops", "loops", "Start a loop", "write:loops", "CreateLoopRequest", false},
		{"GET", "/loops/{id}", "loops", "Get a loop", "read:loops", "", false},
		{"GET", "/loops/{id}/stream", "loops", "Stream loop progress (SSE)", "read:loops", "", false},
		{"PUT", "/loops/{id}/pause", "loops", "Pause a loop", "write:loops", "", false},
		{"PUT", "/loops/{id}/resume", "loops", "Resume a loop", "write:loops", "", false},
		{"DELETE", "/loops/{id}", "loops", "Stop a loop", "write:loops", "", false},

		// Filesystem helpers.
		{"GET", "/browse", "system", "Browse a local filesystem path", "read:repos", "", false},
		{"GET", "/git-info", "system", "Inspect a local git repository", "read:repos", "", false},

		// Config.
		{"GET", "/config/secrets", "config", "List secrets", "read:config", "", true},
		{"POST", "/config/secrets", "config", "Create a secret", "write:config", "CreateSecretRequest", false},
		{"DELETE", "/config/secrets/{id}", "config", "Delete a secret", "write:config", "", false},
		{"GET", "/config/plugins", "config", "List plugins", "read:config", "", true},
		{"POST", "/config/plugins/{name}/install", "config", "Install a plugin", "write:config", "", false},
		{"GET", "/config/setup", "config", "Get setup status", "read:config", "", false},

		// Scanners.
		{"GET", "/scanners/tools", "scanners", "List scanner tools", "read:config", "", true},
		{"GET", "/scanners/tools/{name}", "scanners", "Get scanner tool", "read:config", "", false},
		{"POST", "/scanners/tools/check-updates", "scanners", "Check scanner tool updates", "write:config", "", true},
		{"POST", "/scanners/tools/{name}/check-update", "scanners", "Check one scanner tool update", "write:config", "", false},
		{"GET", "/scanners/images", "scanners", "List scanner images", "read:config", "", true},
		{"POST", "/scanners/images/pull", "scanners", "Pull one scanner image", "write:config", "", false},
		{"GET", "/scanners/config", "scanners", "Get scanner config", "read:config", "", false},
		{"GET", "/scanners/list", "scanners", "List scanners", "read:config", "", true},
		{"POST", "/scanners/plan", "scanners", "Explain scanner run and skip decisions", "read:config", "ScannerPlanRequest", false},
		{"POST", "/scanners/doctor", "scanners", "Run scanner diagnostics", "write:config", "", false},
		{"POST", "/scanners/pull", "scanners", "Pull all scanner images", "write:config", "", false},

		// Settings.
		{"GET", "/settings", "config", "Get settings", "read:config", "", false},
		{"PUT", "/settings", "config", "Update settings", "write:config", "", false},

		// AI prompts & providers.
		{"GET", "/ai-prompts", "ai", "List AI prompt templates", "read:config", "", true},
		{"PUT", "/ai-prompts", "ai", "Upsert an AI prompt template", "write:config", "", false},
		{"GET", "/ai-prompts/defaults", "ai", "Get default AI prompts", "read:config", "", false},
		{"POST", "/ai-prompts/preview", "ai", "Preview a rendered AI prompt", "read:config", "", false},
		{"DELETE", "/ai-prompts/{id}", "ai", "Delete an AI prompt template", "write:config", "", false},
		{"GET", "/ai-providers", "ai", "List configured AI providers", "read:config", "", true},
	}
}

var pathParamRe = regexp.MustCompile(`\{([^}]+)\}`)

// SpecJSON builds and returns the OpenAPI 3.0 document as JSON bytes.
func SpecJSON() []byte {
	paths := map[string]any{}
	for _, ep := range Endpoints() {
		op := buildOperation(ep)
		key := ep.Path
		entry, ok := paths[key].(map[string]any)
		if !ok {
			entry = map[string]any{}
			if params := pathParameters(key); len(params) > 0 {
				entry["parameters"] = params
			}
			paths[key] = entry
		}
		entry[strings.ToLower(ep.Method)] = op
	}

	doc := map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":       "wolf API",
			"version":     Version,
			"description": "The wolf code-scanning platform API. Every endpoint here is also reachable from the `wolf` CLI. Authenticate with a JWT (interactive login) or a `wolf_`-prefixed API token (Authorization: Bearer).",
		},
		"servers": []any{
			map[string]any{"url": "/api/v1"},
		},
		"tags":       buildTags(),
		"paths":      paths,
		"components": buildComponents(),
	}
	b, _ := json.MarshalIndent(doc, "", "  ")
	return b
}

func buildTags() []any {
	order := []string{"system", "auth", "tokens", "audit", "users", "repos", "nodes", "collections", "scans", "findings", "fixes", "loops", "config", "scanners", "ai"}
	tags := make([]any, 0, len(order))
	for _, t := range order {
		tags = append(tags, map[string]any{"name": t})
	}
	return tags
}

func pathParameters(path string) []any {
	matches := pathParamRe.FindAllStringSubmatch(path, -1)
	params := make([]any, 0, len(matches))
	for _, m := range matches {
		params = append(params, map[string]any{
			"name":     m[1],
			"in":       "path",
			"required": true,
			"schema":   map[string]any{"type": "string"},
		})
	}
	return params
}

func buildOperation(ep Endpoint) map[string]any {
	op := map[string]any{
		"tags":        []any{ep.Tag},
		"summary":     ep.Summary,
		"operationId": operationID(ep),
		"responses":   buildResponses(ep),
	}
	if ep.Scope != "" {
		op["security"] = []any{map[string]any{"BearerAuth": []any{}}}
		desc := "Requires authentication."
		if ep.Scope != "self" {
			desc = "Required scope: `" + ep.Scope + "`."
		}
		op["description"] = desc
	}
	if ep.Body != "" {
		op["requestBody"] = map[string]any{
			"required": true,
			"content": map[string]any{
				"application/json": map[string]any{
					"schema": map[string]any{"$ref": "#/components/schemas/" + ep.Body},
				},
			},
		}
	}
	return op
}

func operationID(ep Endpoint) string {
	id := strings.ToLower(ep.Method) + " " + ep.Path
	id = strings.NewReplacer("/", " ", "{", "", "}", "", "-", " ").Replace(id)
	parts := strings.Fields(id)
	for i := 1; i < len(parts); i++ {
		parts[i] = strings.Title(parts[i]) //nolint:staticcheck // ASCII identifiers only
	}
	return strings.Join(parts, "")
}

func buildResponses(ep Endpoint) map[string]any {
	okSchema := "SuccessResponse"
	if ep.List {
		okSchema = "ListResponse"
	}
	okCode := "200"
	if ep.Method == "POST" && ep.Body != "" {
		okCode = "201"
	}
	if ep.Method == "DELETE" {
		okCode = "204"
	}
	resp := map[string]any{}
	if okCode == "204" {
		resp["204"] = map[string]any{"description": "No content"}
	} else {
		resp[okCode] = map[string]any{
			"description": "Success",
			"content": map[string]any{
				"application/json": map[string]any{
					"schema": map[string]any{"$ref": "#/components/schemas/" + okSchema},
				},
			},
		}
	}
	if ep.Body != "" {
		resp["400"] = errRef("Invalid request")
	}
	if ep.Scope != "" {
		resp["401"] = errRef("Missing or invalid credential")
		if ep.Scope != "self" {
			resp["403"] = errRef("Insufficient scope")
		}
	}
	if strings.Contains(ep.Path, "{") {
		resp["404"] = errRef("Resource not found")
	}
	resp["429"] = errRef("Rate limited")
	return resp
}

func errRef(desc string) map[string]any {
	return map[string]any{
		"description": desc,
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": map[string]any{"$ref": "#/components/schemas/ErrorResponse"},
			},
		},
	}
}

func buildComponents() map[string]any {
	str := map[string]any{"type": "string"}
	strArr := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	return map[string]any{
		"securitySchemes": map[string]any{
			"BearerAuth": map[string]any{
				"type":        "http",
				"scheme":      "bearer",
				"description": "A JWT from POST /auth/login, or a wolf_ API token from POST /auth/tokens.",
			},
		},
		"schemas": map[string]any{
			"SuccessResponse": map[string]any{
				"type":       "object",
				"properties": map[string]any{"data": map[string]any{"type": "object"}},
			},
			"ListResponse": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"data": map[string]any{"type": "array", "items": map[string]any{"type": "object"}},
					"meta": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"total":    map[string]any{"type": "integer"},
							"page":     map[string]any{"type": "integer"},
							"per_page": map[string]any{"type": "integer"},
						},
					},
				},
			},
			"ErrorResponse": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"error": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"code":    str,
							"message": str,
						},
					},
				},
			},
			"RegisterRequest": objSchema(map[string]any{"email": str, "password": str}, "email", "password"),
			"LoginRequest":    objSchema(map[string]any{"email": str, "password": str}, "email", "password"),
			"ChangePasswordRequest": objSchema(map[string]any{
				"current_password": str, "new_password": str,
			}, "current_password", "new_password"),
			"CreateTokenRequest": objSchema(map[string]any{
				"name":            str,
				"scopes":          strArr,
				"expires_in_days": map[string]any{"type": "integer", "description": "Omit for the 90-day default; 0 means never expires."},
			}, "name", "scopes"),
			"CreateUserRequest": objSchema(map[string]any{"email": str, "password": str}, "email"),
			"CreateRepoRequest": objSchema(map[string]any{
				"name": str, "source_type": str, "source_path": str, "remote_node_id": str, "remote_path": str, "default_branch": str,
			}, "name", "source_path"),
			"UpdateRepoRequest": objSchema(map[string]any{"name": str, "branch": str}),
			"RemoteNodeRequest": objSchema(map[string]any{
				"name": str, "host": str, "port": map[string]any{"type": "integer"}, "username": str,
				"auth_type": str, "credential_secret_id": str, "known_hosts": str, "base_path": str,
				"enabled": map[string]any{"type": "boolean"},
			}, "name", "host", "username"),
			"CreateCollectionRequest": objSchema(map[string]any{"name": str}, "name"),
			"CollectionRepoRequest":   objSchema(map[string]any{"repo_id": str}, "repo_id"),
			"CreateScanRequest": objSchema(map[string]any{
				"repo_id": str, "collection_id": str, "branch": str, "tools": strArr,
			}),
			"FindingStatusRequest": objSchema(map[string]any{"status": str}, "status"),
			"CreateFixRequest":     objSchema(map[string]any{"finding_id": str, "scan_id": str}),
			"CreateLoopRequest":    objSchema(map[string]any{"repo_id": str}),
			"CreateSecretRequest": objSchema(map[string]any{
				"key_type": str, "key_name": str, "value": str,
			}, "key_name", "value"),
		},
	}
}

func objSchema(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		req := make([]any, len(required))
		for i, r := range required {
			req[i] = r
		}
		s["required"] = req
	}
	return s
}
