package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/alphabravocompany/thewolf/internal/api/response"
)

// MCPStatus handles GET /mcp/status. MCP is default off.
func MCPStatus(w http.ResponseWriter, r *http.Request) {
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]any{"enabled": mcpEnabled()}})
}

type mcpInput struct {
	ID              string            `json:"id,omitempty"`
	Query           map[string]string `json:"query,omitempty"`
	Body            map[string]any    `json:"body,omitempty"`
	IdempotencyKey  string            `json:"idempotency_key,omitempty"`
	VulnerabilityID string            `json:"vulnerability_id,omitempty"`
}

type mcpRequestKey struct{}

// NewMCPHandler adapts fixed operations through the actual API router. Each
// call gets the same scopes, ownership, validation, audit and job persistence
// as REST/CLI. Credentials come from this request, never a previous session.
func NewMCPHandler(api http.Handler) http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: "wolf", Version: AppVersion}, nil)
	for _, definition := range mcpTools() {
		name := definition["name"].(string)
		method, path, _ := strings.Cut(definition["rest"].(string), " ")
		readOnly := method == http.MethodGet || path == "/scans/preflight"
		mcp.AddTool(server, &mcp.Tool{
			Name: name, Description: definition["description"].(string), InputSchema: definition["inputSchema"],
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly, IdempotentHint: readOnly || name == "wolf.scans.create" || name == "wolf.scans.retry" || name == "wolf.fixes.create"},
		}, func(ctx context.Context, req *mcp.CallToolRequest, input mcpInput) (*mcp.CallToolResult, any, error) {
			body, status, err := mcpAPI(ctx, api, req.Extra, method, path, input)
			if err != nil {
				return nil, nil, err
			}
			result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(body)}}, IsError: status >= 400}
			return result, nil, nil
		})
	}
	for _, definition := range mcpResources() {
		uri, name := definition["uri"].(string), definition["name"].(string)
		server.AddResource(&mcp.Resource{URI: uri, Name: name, MIMEType: "application/json"},
			func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
				body, status, err := mcpAPI(ctx, api, req.Extra, http.MethodGet, "/"+name, mcpInput{})
				if err != nil {
					return nil, err
				}
				if status >= 400 {
					return nil, fmt.Errorf("API returned HTTP %d: %s", status, body)
				}
				return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: "application/json", Text: string(body)}}}, nil
			})
	}
	transport := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !mcpEnabled() {
			response.WriteError(w, http.StatusNotFound, "mcp_disabled", "MCP is off (set WOLF_MCP_ENABLED=1)")
			return
		}
		transport.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), mcpRequestKey{}, r)))
	})
}

func mcpAPI(ctx context.Context, api http.Handler, extra *mcp.RequestExtra, method, path string, input mcpInput) ([]byte, int, error) {
	if method == http.MethodPost && path == "/fixes" && strings.TrimSpace(input.IdempotencyKey) == "" {
		return nil, 0, fmt.Errorf("idempotency_key is required for fix creation")
	}

	if strings.Contains(path, "{id}") {
		if input.ID == "" || strings.ContainsAny(input.ID, "/\\?#%") || input.ID == "." || input.ID == ".." {
			return nil, 0, fmt.Errorf("invalid resource id")
		}
		path = strings.ReplaceAll(path, "{id}", url.PathEscape(input.ID))
	}
	query := url.Values{}
	for key, value := range input.Query {
		query.Set(key, value)
	}
	if input.VulnerabilityID != "" {
		query.Set("vulnerability_id", input.VulnerabilityID)
	}
	if method == http.MethodGet && query.Get("per_page") == "" {
		query.Set("per_page", "50")
	}
	body, err := json.Marshal(input.Body)
	if err != nil {
		return nil, 0, err
	}
	// Clear the outer chi route context before entering the router again.
	ctx = context.WithValue(ctx, chi.RouteCtxKey, chi.NewRouteContext())
	req, err := http.NewRequestWithContext(ctx, method, "/api/v1"+path+"?"+query.Encode(), bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	if original, ok := ctx.Value(mcpRequestKey{}).(*http.Request); ok {
		req.Host, req.RemoteAddr, req.TLS = original.Host, original.RemoteAddr, original.TLS
	}
	req.RequestURI = req.URL.RequestURI()
	if extra != nil {
		for _, key := range []string{"Authorization", "Cookie", "Origin", "X-Forwarded-Proto", "Traceparent", "X-Wolf-Operation-ID"} {
			for _, value := range extra.Header.Values(key) {
				req.Header.Add(key, value)
			}
		}
	}
	req.Header.Set("Content-Type", "application/json")
	if input.IdempotencyKey != "" {
		req.Header.Set("Idempotency-Key", input.IdempotencyKey)
	}
	recorder := httptest.NewRecorder()
	api.ServeHTTP(recorder, req)
	return recorder.Body.Bytes(), recorder.Code, nil
}

func mcpResources() []map[string]any {
	var resources []map[string]any
	for _, name := range []string{"edition", "coverage", "license", "scan-profiles"} {
		resources = append(resources, map[string]any{"uri": "wolf://" + name, "name": name, "mimeType": "application/json"})
	}
	return resources
}

func mcpTools() []map[string]any {
	definitions := []struct{ name, description, rest string }{
		{"wolf.repos.list", "List accessible repositories; query accepts REST filters and pagination", "GET /repos"},
		{"wolf.scans.list", "List accessible scans; query accepts REST filters and pagination", "GET /scans"},
		{"wolf.scans.get", "Read scan execution status and coverage", "GET /scans/{id}"},
		{"wolf.scans.result", "Read a scan result", "GET /scans/{id}/result"},
		{"wolf.scans.preflight", "Preview scanner selection and prerequisites using a scan request body", "POST /scans/preflight"},
		{"wolf.scans.create", "Enqueue a scan using the REST request body; reuse idempotency_key when retrying the same request", "POST /scans"},
		{"wolf.scans.cancel", "Cancel a scan; preserves findings already collected", "DELETE /scans/{id}"},
		{"wolf.scans.retry", "Retry a failed or cancelled scan", "POST /scans/{id}/retry"},
		{"wolf.scans.gate", "Evaluate findings and execution completeness; only pass/warn permits CI success", "GET /scans/{id}/gate"},
		{"wolf.scans.test-coverage", "Read measured automated test coverage and coverage policy checks", "GET /scans/{id}/test-coverage"},
		{"wolf.scans.coverage-files", "Read covered and uncovered source files; query supports pagination, q, metric, sort and order", "GET /scans/{id}/test-coverage/files"},
		{"wolf.scans.import-coverage", "Replace measured coverage with a complete report bundle for the exact scanned commit; body requires commit_sha and reports", "PUT /scans/{id}/test-coverage"},
		{"wolf.policies.list", "Read accessible quality gate policies", "GET /policies"},
		{"wolf.policies.create", "Create a quality gate policy using the REST request body", "POST /policies"},
		{"wolf.policies.update", "Update a quality gate policy using the REST request body", "PUT /policies/{id}"},
		{"wolf.findings.list", "List findings; query accepts REST filters and pagination", "GET /findings"},
		{"wolf.findings.get", "Read finding evidence and remediation context", "GET /findings/{id}"},
		{"wolf.vulnerabilities.list", "List canonical vulnerabilities", "GET /vulnerabilities"},
		{"wolf.edition.get", "Read edition and entitlements", "GET /edition"},
		{"wolf.coverage.get", "Read scanner coverage", "GET /coverage"},
		{"wolf.license.get", "Read license status", "GET /license"},
		{"wolf.scan-profiles.get", "List scan profiles", "GET /scan-profiles"},
		{"wolf.evidence.list", "Read evidence for a vulnerability", "GET /evidence"},
		{"wolf.fixes.create", "Create a fix job with a durable idempotency_key reused on retry. Default dry_run retains a review branch; explicit mode push also publishes it and opens a GitHub PR. Requires fixing enabled, write:fixes and ownership of the selected repository/scan/findings.", "POST /fixes"},
		{"wolf.fixes.list", "List remediation jobs", "GET /fixes"},
		{"wolf.fixes.get", "Read remediation progress", "GET /fixes/{id}"},
		{"wolf.fixes.diff", "Read a remediation diff before deciding to push", "GET /fixes/{id}/diff"},
	}
	var tools []map[string]any
	for _, d := range definitions {
		properties := map[string]any{"query": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}}}
		required := []string{}
		if strings.Contains(d.rest, "{id}") {
			properties["id"] = map[string]any{"type": "string", "minLength": 1}
			required = append(required, "id")
		}
		if d.name == "wolf.fixes.create" || d.name == "wolf.scans.create" || d.name == "wolf.scans.preflight" || d.name == "wolf.scans.import-coverage" || d.name == "wolf.policies.create" || d.name == "wolf.policies.update" {
			properties["body"] = map[string]any{"type": "object", "description": "The JSON body accepted by the corresponding REST endpoint"}
			required = append(required, "body")
		}
		if d.name == "wolf.fixes.create" || d.name == "wolf.scans.create" || d.name == "wolf.scans.retry" {
			properties["idempotency_key"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 255}
			required = append(required, "idempotency_key")
		}
		if d.name == "wolf.evidence.list" {
			properties["vulnerability_id"] = map[string]any{"type": "string", "minLength": 1}
			required = append(required, "vulnerability_id")
		}
		tools = append(tools, map[string]any{"name": d.name, "description": d.description, "rest": d.rest,
			"inputSchema": map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}})
	}
	return tools
}
