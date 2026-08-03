package scannerdiscovery

import (
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/scannertools/manifest"
)

func TestRedactEvidenceRemovesCredentials(t *testing.T) {
	input := Evidence{
		SourceURL: "https://user:password@example.invalid/releases?token=abc&safe=yes&X-Amz-Signature=xyz#fragment",
		Reference: "Authorization=super-secret",
		ETag:      "Bearer opaque-token",
		Detail:    "failure\napi_key=key123 password: pass123",
		Attributes: map[string]string{
			"authorization": "Bearer another-token",
			"request_id":    "safe",
			"callback":      "https://name:pass@example.invalid/path",
		},
	}
	got := RedactEvidence(input)
	encoded := strings.Join([]string{
		got.SourceURL, got.Reference, got.ETag, got.Detail,
		got.Attributes["authorization"], got.Attributes["callback"],
	}, " ")
	for _, secret := range []string{"abc", "xyz", "super-secret", "opaque-token", "key123", "pass123", "another-token", "name:pass"} {
		if strings.Contains(encoded, secret) {
			t.Fatalf("redacted evidence contains %q: %+v", secret, got)
		}
	}
	if !strings.Contains(got.SourceURL, "safe=yes") || got.Attributes["request_id"] != "safe" {
		t.Fatalf("safe evidence was lost: %+v", got)
	}
}

func TestRedactTextNormalizesControlsAndCapsLength(t *testing.T) {
	got := RedactText("bad\nheader\t" + strings.Repeat("x", 1200))
	if strings.ContainsAny(got, "\n\t") || len(got) > 1004 || !strings.HasSuffix(got, "…") {
		t.Fatalf("unexpected redacted text length/content: %d %q", len(got), got[:20])
	}
}

func TestSensitiveAttributeDetection(t *testing.T) {
	for _, key := range []string{"access_token", "X-Api-Key", "registry.password", "privateKey", "cookie"} {
		if !sensitiveKey(key) {
			t.Fatalf("%q was not classified sensitive", key)
		}
	}
	if sensitiveKey("request_id") {
		t.Fatal("request_id was classified sensitive")
	}
}

func TestRedactItemRemovesRuntimeDefinitionAndSensitiveMetadata(t *testing.T) {
	item := Item{
		CurrentValue:   "1.2.3",
		Source:         Source{URL: "https://example.invalid?token=secret"},
		ToolDefinition: &manifest.Tool{DisplayName: "internal definition"},
		Metadata: map[string]string{
			"registry_token": "secret", "category": "sast",
		},
	}
	got := RedactItem(item)
	if got.ToolDefinition != nil || got.Metadata["registry_token"] != redacted ||
		got.Metadata["category"] != "sast" || strings.Contains(got.Source.URL, "secret") {
		t.Fatalf("redacted item = %+v", got)
	}
}
