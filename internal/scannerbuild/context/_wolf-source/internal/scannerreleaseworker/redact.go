package scannerreleaseworker

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

const maxEvidenceText = 4096

var (
	bearerRE  = regexp.MustCompile(`(?i)\b(bearer\s+)[A-Za-z0-9._~+/=-]+`)
	secretRE  = regexp.MustCompile(`(?i)\b(token|password|passwd|secret|api[_-]?key|authorization|cookie)\s*[:=]\s*([^\s,;]+)`)
	urlUserRE = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^/@\s]+@`)
)

func redactText(value string) string {
	value = bearerRE.ReplaceAllString(value, `${1}[REDACTED]`)
	value = secretRE.ReplaceAllString(value, `${1}=[REDACTED]`)
	value = urlUserRE.ReplaceAllString(value, `${1}[REDACTED]@`)
	if len(value) > maxEvidenceText {
		return value[:maxEvidenceText] + "…"
	}
	return value
}

func redactURI(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" {
		return redactText(value)
	}
	if parsed.User != nil {
		parsed.User = url.User("[REDACTED]")
	}
	query := parsed.Query()
	for key := range query {
		if sensitiveKey(key) {
			query.Set(key, "[REDACTED]")
		}
	}
	parsed.RawQuery = query.Encode()
	return redactText(parsed.String())
}

func redactValue(key string, value any) any {
	if sensitiveKey(key) {
		return "[REDACTED]"
	}
	switch typed := value.(type) {
	case string:
		return redactText(typed)
	case map[string]any:
		out := make(map[string]any, len(typed))
		for childKey, child := range typed {
			out[childKey] = redactValue(childKey, child)
		}
		return out
	case map[string]string:
		out := make(map[string]any, len(typed))
		for childKey, child := range typed {
			out[childKey] = redactValue(childKey, child)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, child := range typed {
			out[index] = redactValue("", child)
		}
		return out
	case []string:
		out := make([]any, len(typed))
		for index, child := range typed {
			out[index] = redactValue("", child)
		}
		return out
	case fmt.Stringer:
		return redactText(typed.String())
	default:
		return value
	}
}

func sensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	switch normalized {
	case "token", "access_token", "refresh_token", "password", "passwd",
		"secret", "client_secret", "api_key", "apikey", "authorization",
		"cookie", "set_cookie", "credential", "credentials", "private_key":
		return true
	default:
		return strings.HasSuffix(normalized, "_token") ||
			strings.HasSuffix(normalized, "_password") ||
			strings.HasSuffix(normalized, "_secret")
	}
}
