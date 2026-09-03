package routes

import (
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/api/openapi"
)

func TestMCPResourcesCoverEditionCoverageLicense(t *testing.T) {
	got := map[string]bool{}
	for _, r := range mcpResources() {
		uri, _ := r["uri"].(string)
		got[uri] = true
	}
	for _, uri := range []string{"wolf://edition", "wolf://coverage", "wolf://license", "wolf://scan-profiles"} {
		if !got[uri] {
			t.Fatalf("missing %s", uri)
		}
	}
}

func TestMCPToolsHaveRESTCounterparts(t *testing.T) {
	rest := map[string]bool{}
	for _, ep := range openapi.Endpoints() {
		rest[strings.ToUpper(ep.Method)+" "+ep.Path] = true
	}
	for _, tool := range mcpTools() {
		path, _ := tool["rest"].(string)
		if path == "" || !rest[path] {
			t.Fatalf("mcp %s missing OpenAPI %q", tool["name"], path)
		}
	}
}
