package scannerregistry

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRegistryRedirectBoundary(t *testing.T) {
	for _, name := range []string{"same-origin", "different-port", "bearer-retry", "method-change", "loop", "caller-policy"} {
		t.Run(name, func(t *testing.T) {
			var escaped, final atomic.Int32
			other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				escaped.Add(1)
				w.WriteHeader(http.StatusOK)
			}))
			defer other.Close()
			var base string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/token" {
					_, _ = w.Write([]byte(`{"token":"retry-secret"}`))
					return
				}
				if name == "bearer-retry" && r.Header.Get("Authorization") != "Bearer retry-secret" {
					w.Header().Set("WWW-Authenticate", `Bearer realm="`+base+`/token"`)
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				if r.URL.Path == "/done" {
					final.Add(1)
					if r.Header.Get("Authorization") != "Basic private-credential" {
						t.Error("same-origin redirect lost authorization")
					}
					return
				}
				target, status := "/done", http.StatusTemporaryRedirect
				switch name {
				case "different-port", "bearer-retry":
					target = other.URL
				case "loop":
					target = "/v2/"
				case "method-change":
					status = http.StatusSeeOther
				}
				http.Redirect(w, r, target, status)
			}))
			defer server.Close()
			base = server.URL
			host := strings.TrimPrefix(base, "http://")
			httpClient := server.Client()
			var policyCalls atomic.Int32
			if name == "caller-policy" {
				httpClient.CheckRedirect = func(*http.Request, []*http.Request) error {
					policyCalls.Add(1)
					return errors.New("caller denied redirect")
				}
			}
			client := Client{
				HTTP: httpClient, Endpoints: map[string]Endpoint{host: {BaseURL: base}},
				Credentials: CredentialProviderFunc(func(context.Context, string) (string, error) {
					return "Basic private-credential", nil
				}),
			}
			method := http.MethodGet
			if name == "method-change" {
				method = http.MethodPost
			}
			request, err := http.NewRequestWithContext(t.Context(), method, base+"/v2/", nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := client.doAuthorized(t.Context(), request, host)
			if response != nil {
				response.Body.Close()
			}
			if (err == nil) != (name == "same-origin") {
				t.Fatalf("redirect result: %v", err)
			}
			if escaped.Load() != 0 {
				t.Fatal("contacted an unconfigured origin")
			}
			if name == "same-origin" && final.Load() != 1 {
				t.Fatal("same-origin redirect did not complete")
			}
			if name == "caller-policy" && policyCalls.Load() != 1 {
				t.Fatal("caller redirect policy was bypassed")
			}
			if name != "caller-policy" && httpClient.CheckRedirect != nil {
				t.Fatal("shared HTTP client was mutated")
			}
		})
	}
}
