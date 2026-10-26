package scannerregistry

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRegistryJSONRejectsOversizeAndTrailingData(t *testing.T) {
	for _, kind := range []string{"referrers", "token"} {
		for _, payload := range []string{"valid", "trailing-json", "oversized-padding"} {
			t.Run(kind+"/"+payload, func(t *testing.T) {
				body, limit := `{"manifests":[]}`, 64
				if kind == "token" {
					body, limit = `{"token":"secret"}`, 1<<20
				}
				switch payload {
				case "trailing-json":
					body += `{}`
				case "oversized-padding":
					body += strings.Repeat(" ", limit)
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = w.Write([]byte(body))
				}))
				defer server.Close()
				host := strings.TrimPrefix(server.URL, "http://")
				client := Client{HTTP: server.Client(), Endpoints: map[string]Endpoint{host: {BaseURL: server.URL}}, MaxManifestBytes: 64}
				var err error
				if kind == "token" {
					_, err = client.exchangeBearerChallenge(t.Context(), host, "", `Bearer realm="`+server.URL+`/token"`)
				} else {
					_, err = client.FetchReferrers(t.Context(), Reference{Registry: host, Repository: "repo", Digest: "sha256:" + strings.Repeat("a", 64)})
				}
				if (err == nil) != (payload == "valid") {
					t.Fatalf("invalid response handling: %v", err)
				}
			})
		}
	}
}
