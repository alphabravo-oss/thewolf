package scannerdiscovery

import (
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

const redacted = "[REDACTED]"

var (
	bearerPattern        = regexp.MustCompile(`(?i)\bbearer\s+[a-z0-9._~+/=-]+`)
	assignmentPattern    = regexp.MustCompile(`(?i)\b(authorization|password|passwd|secret|token|api[_-]?key|cookie)(\s*[:=]\s*)([^\s,;]+)`)
	credentialURLPattern = regexp.MustCompile(`(?i)(https?://)([^/@\s]+)@`)
)

// RedactEvidence removes credentials and caps persistence size. Resolvers should
// store response digests and selected headers, never raw response bodies.
func RedactEvidence(evidence Evidence) Evidence {
	out := Evidence{
		SourceURL:      RedactURL(evidence.SourceURL),
		Reference:      RedactText(evidence.Reference),
		ResponseDigest: RedactText(evidence.ResponseDigest),
		ETag:           RedactText(evidence.ETag),
		LastModified:   RedactText(evidence.LastModified),
		Detail:         RedactText(evidence.Detail),
	}
	if len(evidence.Attributes) > 0 {
		out.Attributes = make(map[string]string, len(evidence.Attributes))
		keys := make([]string, 0, len(evidence.Attributes))
		for key := range evidence.Attributes {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if sensitiveKey(key) {
				out.Attributes[key] = redacted
			} else {
				out.Attributes[key] = RedactText(evidence.Attributes[key])
			}
		}
	}
	return out
}

// RedactItem returns the persistence-safe form of a discovery input. The
// in-memory manifest definition is removed and all operator-supplied metadata
// passes through the same credential filtering as evidence.
func RedactItem(item Item) Item {
	out := item
	out.ToolDefinition = nil
	out.CurrentValue = RedactText(item.CurrentValue)
	out.CurrentDigest = RedactText(item.CurrentDigest)
	out.Source.URL = RedactURL(item.Source.URL)
	out.Source.Reference = RedactText(item.Source.Reference)
	out.Source.Host = RedactText(item.Source.Host)
	if len(item.Metadata) > 0 {
		out.Metadata = make(map[string]string, len(item.Metadata))
		for key, value := range item.Metadata {
			if sensitiveKey(key) {
				out.Metadata[key] = redacted
			} else {
				out.Metadata[key] = RedactText(value)
			}
		}
	}
	return out
}

func RedactURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return RedactText(raw)
	}
	if parsed.User != nil {
		parsed.User = url.User(redacted)
	}
	query := parsed.Query()
	for key := range query {
		if sensitiveKey(key) || strings.HasPrefix(strings.ToLower(key), "x-amz-") {
			query.Set(key, redacted)
		}
	}
	parsed.RawQuery = query.Encode()
	parsed.Fragment = ""
	return RedactText(parsed.String())
}

func RedactText(value string) string {
	value = strings.Map(func(char rune) rune {
		if unicode.IsControl(char) {
			return ' '
		}
		return char
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	value = bearerPattern.ReplaceAllString(value, "Bearer "+redacted)
	value = assignmentPattern.ReplaceAllString(value, `${1}${2}`+redacted)
	value = credentialURLPattern.ReplaceAllString(value, `${1}`+redacted+"@")
	const maximum = 1000
	if len(value) > maximum {
		value = value[:maximum] + "…"
	}
	return value
}

func sensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.NewReplacer("-", "", "_", "", ".", "").Replace(key))
	for _, token := range []string{
		"authorization", "password", "passwd", "secret", "token", "apikey",
		"cookie", "credential", "signature", "privatekey",
	} {
		if strings.Contains(normalized, token) {
			return true
		}
	}
	return false
}
