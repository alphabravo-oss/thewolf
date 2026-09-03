package routes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/pkg/edition"
	"github.com/alphabravocompany/thewolf/pkg/entitlement"
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// MCPStatus handles GET /mcp/status. MCP is default off.
func MCPStatus(w http.ResponseWriter, r *http.Request) {
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: map[string]any{"enabled": mcpEnabled()},
	})
}

// HandleMCP is JSON-RPC over the existing authenticated API, not the database.
// Disabled unless WOLF_MCP_ENABLED=1.
func HandleMCP(w http.ResponseWriter, r *http.Request) {
	if !mcpEnabled() {
		response.WriteError(w, http.StatusNotFound, "mcp_disabled", "MCP is off (set WOLF_MCP_ENABLED=1)")
		return
	}
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return
	}
	var req rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON-RPC body")
		return
	}
	out := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize", "notifications/initialized":
		out.Result = map[string]any{
			"protocolVersion": "2024-11-05",
			"serverInfo":      map[string]any{"name": "wolf", "version": AppVersion},
			"capabilities": map[string]any{
				"tools":     map[string]any{},
				"resources": map[string]any{},
				"prompts":   map[string]any{},
			},
		}
	case "ping":
		out.Result = map[string]any{}
	case "logging/setLevel":
		out.Result = map[string]any{}
	case "prompts/list":
		out.Result = map[string]any{"prompts": []any{}}
	case "tools/list":
		out.Result = map[string]any{"tools": mcpTools()}
	case "resources/list":
		out.Result = map[string]any{"resources": mcpResources()}
	case "resources/read":
		var p struct {
			URI string `json:"uri"`
		}
		_ = json.Unmarshal(req.Params, &p)
		data, err := mcpResource(r, claims.UserID, p.URI)
		if err != nil {
			out.Error = &rpcError{Code: -32000, Message: err.Error()}
			break
		}
		out.Result = data
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		_ = json.Unmarshal(req.Params, &p)
		result, err := mcpCall(r, claims.UserID, p.Name, p.Arguments)
		if err != nil {
			out.Error = &rpcError{Code: -32000, Message: err.Error()}
			break
		}
		out.Result = result
	default:
		out.Error = &rpcError{Code: -32601, Message: "method not found"}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func mcpCall(r *http.Request, userID, name string, args map[string]any) (any, error) {
	h := DefaultHandler
	if h == nil || h.Store == nil {
		return nil, errMCP("handler not initialized")
	}
	ctx := r.Context()
	switch name {
	case "wolf.repos.list":
		repos, err := h.Store.ListReposByUser(ctx, userID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"content": repos}, nil
	case "wolf.scans.list":
		scans, err := h.Store.ListScansByUser(ctx, userID)
		if err != nil {
			return nil, err
		}
		if len(scans) > 50 {
			scans = scans[:50]
		}
		return map[string]any{"content": scans}, nil
	case "wolf.findings.list":
		findings, err := h.Store.ListCurrentOpenFindings(ctx, userID, false, "")
		if err != nil {
			return nil, err
		}
		if len(findings) > 50 {
			findings = findings[:50]
		}
		return map[string]any{"content": findings}, nil
	case "wolf.vulnerabilities.list":
		vulns, err := h.Store.ListVulnerabilitiesForUser(ctx, userID, false)
		if err != nil {
			return nil, err
		}
		if len(vulns) > 50 {
			vulns = vulns[:50]
		}
		return map[string]any{"content": vulns}, nil
	case "wolf.edition.get":
		granted := map[string]bool{}
		for _, c := range entitlement.Catalog() {
			granted[c] = entitlement.Active().Allows(c)
		}
		return map[string]any{"content": map[string]any{
			"edition":      edition.Default.Name(),
			"entitlements": granted,
			"limits":       entitlement.CommunityLimits(),
		}}, nil
	case "wolf.coverage.get":
		rec := httptest.NewRecorder()
		GetCoverage(rec, r)
		return map[string]any{"content": json.RawMessage(rec.Body.Bytes())}, nil
	case "wolf.license.get":
		return map[string]any{"content": licenseStatus("")}, nil
	case "wolf.scan-profiles.get":
		rec := httptest.NewRecorder()
		ListScanProfiles(rec, r)
		return map[string]any{"content": json.RawMessage(rec.Body.Bytes())}, nil
	case "wolf.evidence.list":
		id, _ := args["vulnerability_id"].(string)
		if strings.TrimSpace(id) == "" {
			return nil, errMCP("vulnerability_id is required")
		}
		req := r.Clone(r.Context())
		q := req.URL.Query()
		q.Set("vulnerability_id", id)
		req.URL.RawQuery = q.Encode()
		rec := httptest.NewRecorder()
		ListEvidence(rec, req)
		if rec.Code >= 400 {
			return nil, errMCP(strings.TrimSpace(rec.Body.String()))
		}
		return map[string]any{"content": json.RawMessage(rec.Body.Bytes())}, nil
	default:
		return nil, errMCP("unknown tool")
	}
}

func mcpResources() []map[string]any {
	return []map[string]any{
		{"uri": "wolf://edition", "name": "edition", "mimeType": "application/json"},
		{"uri": "wolf://coverage", "name": "coverage", "mimeType": "application/json"},
		{"uri": "wolf://license", "name": "license", "mimeType": "application/json"},
		{"uri": "wolf://scan-profiles", "name": "scan-profiles", "mimeType": "application/json"},
	}
}

func mcpResource(r *http.Request, userID, uri string) (any, error) {
	switch uri {
	case "wolf://edition":
		return mcpCall(r, userID, "wolf.edition.get", nil)
	case "wolf://coverage":
		return mcpCall(r, userID, "wolf.coverage.get", nil)
	case "wolf://license":
		return mcpCall(r, userID, "wolf.license.get", nil)
	case "wolf://scan-profiles":
		return mcpCall(r, userID, "wolf.scan-profiles.get", nil)
	default:
		return nil, errMCP("unknown resource")
	}
}

func mcpTools() []map[string]any {
	empty := map[string]any{"type": "object", "properties": map[string]any{}}
	return []map[string]any{
		{"name": "wolf.repos.list", "description": "List repositories the caller can read", "rest": "GET /repos", "inputSchema": empty},
		{"name": "wolf.scans.list", "description": "List scans the caller can read", "rest": "GET /scans", "inputSchema": empty},
		{"name": "wolf.findings.list", "description": "List current-open findings the caller can read", "rest": "GET /findings", "inputSchema": empty},
		{"name": "wolf.vulnerabilities.list", "description": "List canonical vulnerabilities dual-written from findings", "rest": "GET /vulnerabilities", "inputSchema": empty},
		{"name": "wolf.edition.get", "description": "Edition, entitlements, and Community limits", "rest": "GET /edition", "inputSchema": empty},
		{"name": "wolf.coverage.get", "description": "Honest scanner coverage matrix from tools.yaml", "rest": "GET /coverage", "inputSchema": empty},
		{"name": "wolf.license.get", "description": "Commercial license status (Community has none)", "rest": "GET /license", "inputSchema": empty},
		{"name": "wolf.scan-profiles.get", "description": "Named scan profiles", "rest": "GET /scan-profiles", "inputSchema": empty},
		{"name": "wolf.evidence.list", "description": "Evidence members for a vulnerability", "rest": "GET /evidence",
			"inputSchema": map[string]any{"type": "object", "required": []any{"vulnerability_id"}, "properties": map[string]any{"vulnerability_id": map[string]any{"type": "string"}}}},
	}
}

type mcpError string

func (e mcpError) Error() string { return string(e) }

func errMCP(s string) error { return mcpError(s) }
