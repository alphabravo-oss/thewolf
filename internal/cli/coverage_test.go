package cli

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/alphabravocompany/thewolf/internal/api/openapi"
)

var covBraceRe = regexp.MustCompile(`\{[^}]+\}`)

// normEndpoint collapses every path parameter to "{}" so CLI templates and
// OpenAPI paths compare regardless of the parameter's name.
func normEndpoint(method, path string) string {
	return strings.ToUpper(method) + " " + covBraceRe.ReplaceAllString(path, "{}")
}

// cliExempt lists endpoints intentionally without a dedicated `wolf` command,
// each with the reason. These are interactive/browser flows or machine scrape
// endpoints where a rendered CLI command would make no sense. Everything else
// MUST have a command.
var cliExempt = map[string]string{
	"GET /metrics":            "Prometheus scrapes the public text endpoint directly",
	"POST /auth/register":     "self-service signup is a browser flow",
	"GET /auth/settings":      "public bootstrap info for the login page",
	"POST /auth/mfa/login":    "part of the interactive `wolf auth login` challenge",
	"GET /auth/mfa/status":    "two-factor enrollment is a browser/QR flow",
	"POST /auth/mfa/setup":    "two-factor enrollment is a browser/QR flow",
	"POST /auth/mfa/activate": "two-factor enrollment is a browser/QR flow",
	"POST /auth/mfa/disable":  "two-factor enrollment is a browser/QR flow",
	"POST /webhooks/github":   "inbound GitHub HMAC webhook",

	"GET /auth/sso/{}/start":    "browser SSO redirect",
	"GET /auth/sso/{}/callback": "browser SSO callback",
}

// TestCLICoversEveryEndpoint is the CLI counterpart to the OpenAPI coverage
// test: every documented endpoint must be reachable from a `wolf` command
// (or be on the explicit UI-only exempt list). Commands declare which endpoint
// they hit via apiAnno()/the cobra Annotations the helpers set.
func TestCLICoversEveryEndpoint(t *testing.T) {
	covered := map[string]bool{}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if a := c.Annotations[apiAnnotationKey]; a != "" {
			covered[a] = true
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	for _, g := range APICommandTree() {
		walk(g)
	}

	var missing []string
	for _, ep := range openapi.Endpoints() {
		normPath := covBraceRe.ReplaceAllString(ep.Path, "{}")
		if strings.HasSuffix(normPath, "/stream") {
			continue // SSE streams are covered by `watch`, exercised elsewhere
		}
		key := normEndpoint(ep.Method, ep.Path)
		if _, ok := cliExempt[key]; ok {
			continue
		}
		if !covered[key] {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("the CLI has no command for %d endpoint(s) (add a `wolf` command, or add to cliExempt with a reason):\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}
}
