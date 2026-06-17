package middleware

import (
	"net/http"
	"strings"
)

// Audit categories and severities — a small controlled vocabulary so the log
// can be filtered the way a compliance reviewer thinks ("all authentication
// events", "all critical events").
const (
	CatAuthentication = "authentication"
	CatAuthorization  = "authorization"
	CatConfiguration  = "configuration"
	CatSecrets        = "secrets"
	CatData           = "data"
	CatSystem         = "system"

	SevInfo     = "info"
	SevWarning  = "warning"
	SevCritical = "critical"
)

// auditRule maps a matched (method, path) to a classified event.
type auditRule struct {
	method   string
	match    func(p string) bool
	event    string
	category string
	severity string
}

func eq(want string) func(string) bool { return func(p string) bool { return p == want } }
func underPrefixSuffix(prefix, suffix string) func(string) bool {
	return func(p string) bool { return strings.HasPrefix(p, prefix) && strings.HasSuffix(p, suffix) }
}
func underPrefix(prefix string) func(string) bool {
	return func(p string) bool { return strings.HasPrefix(p, prefix) }
}

// auditRules: security-relevant events get explicit, stable classifications.
// Anything not matched falls through to a sensible default (see Classify).
var auditRules = []auditRule{
	// Authentication
	{http.MethodPost, eq("/auth/mfa/setup"), "auth.mfa.enrolled", CatAuthentication, SevInfo},
	{http.MethodPost, eq("/auth/mfa/activate"), "auth.mfa.enabled", CatAuthentication, SevWarning},
	{http.MethodPost, eq("/auth/mfa/disable"), "auth.mfa.disabled", CatAuthentication, SevCritical},
	{http.MethodPut, eq("/auth/password"), "auth.password.changed", CatAuthentication, SevWarning},
	{http.MethodPut, eq("/auth/profile"), "account.profile.updated", CatAuthentication, SevInfo},
	{http.MethodPost, eq("/auth/logout"), "auth.logout", CatAuthentication, SevInfo},
	// Authorization (users + roles)
	{http.MethodPost, eq("/users"), "user.created", CatAuthorization, SevWarning},
	{http.MethodPut, underPrefixSuffix("/users/", "/role"), "user.role.changed", CatAuthorization, SevCritical},
	{http.MethodPost, underPrefixSuffix("/users/", "/mfa/reset"), "auth.mfa.reset", CatAuthentication, SevCritical},
	{http.MethodDelete, underPrefix("/users/"), "user.deleted", CatAuthorization, SevCritical},
	// Secrets + API keys
	{http.MethodPost, eq("/auth/tokens"), "apikey.created", CatSecrets, SevWarning},
	{http.MethodDelete, underPrefix("/auth/tokens/"), "apikey.revoked", CatSecrets, SevWarning},
	{http.MethodPost, eq("/config/secrets"), "secret.created", CatSecrets, SevWarning},
	{http.MethodDelete, underPrefix("/config/secrets/"), "secret.deleted", CatSecrets, SevWarning},
	// Configuration
	{http.MethodPut, eq("/settings"), "settings.updated", CatConfiguration, SevWarning},
	// System
	{http.MethodPost, underPrefixSuffix("/config/plugins/", "/install"), "plugin.installed", CatSystem, SevWarning},
}

// categoryByResource gives a better default category for a few resources than a
// flat "data" when no explicit rule matched.
var categoryByResource = map[string]string{
	"settings":     CatConfiguration,
	"config":       CatConfiguration,
	"nodes":        CatConfiguration,
	"policies":     CatConfiguration,
	"ai-prompts":   CatConfiguration,
	"ai-providers": CatConfiguration,
	"scanners":     CatSystem,
	"admin":        CatSystem,
	"audit-log":    CatSystem,
}

// Classify maps an HTTP (method, path) to a semantic audit event, category, and
// severity. Inputs come from the router, never raw user content.
func Classify(method, path string) (event, category, severity string) {
	p := normalizeAuditPath(path)
	for _, r := range auditRules {
		if r.method == method && r.match(p) {
			return r.event, r.category, r.severity
		}
	}
	resource := firstSegment(p)
	if resource == "" {
		resource = "request"
	}
	category = categoryByResource[resource]
	if category == "" {
		category = CatData
	}
	severity = SevInfo
	if method == http.MethodDelete {
		severity = SevWarning
	}
	return resource + "." + verbForMethod(method), category, severity
}

// normalizeAuditPath strips the /api or /api/v1 prefix so rules match a clean
// resource path.
func normalizeAuditPath(path string) string {
	for _, prefix := range []string{"/api/v1", "/api"} {
		if strings.HasPrefix(path, prefix) {
			return strings.TrimSuffix(path[len(prefix):], "/")
		}
	}
	return strings.TrimSuffix(path, "/")
}

func firstSegment(p string) string {
	p = strings.TrimPrefix(p, "/")
	if i := strings.IndexByte(p, '/'); i >= 0 {
		return p[:i]
	}
	return p
}

func verbForMethod(method string) string {
	switch method {
	case http.MethodPost:
		return "created"
	case http.MethodPut, http.MethodPatch:
		return "updated"
	case http.MethodDelete:
		return "deleted"
	default:
		return "changed"
	}
}
