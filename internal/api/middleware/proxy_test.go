package middleware

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestForwardedHeaderTrustBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, config, peer, xff, proto, want string
		secure                               bool
	}{
		{"direct", "", "198.51.100.9:1234", "203.0.113.1", "https", "198.51.100.9", false},
		{"loopback is not implicit trust", "", "127.0.0.1:1234", "203.0.113.1", "https", "127.0.0.1", false},
		{"configured loopback", "127.0.0.1", "127.0.0.1:1234", "203.0.113.1", "https", "203.0.113.1", true},
		{"untrusted peer", "10.0.0.0/24", "198.51.100.9:1234", "203.0.113.1", "https", "198.51.100.9", false},
		{"CIDR boundary", "10.0.0.0/24", "10.0.1.1:1234", "203.0.113.1", "https", "10.0.1.1", false},
		{"trusted chain", "10.0.0.0/24", "10.0.0.1:1234", "203.0.113.1, 10.0.0.2", "https", "203.0.113.1", true},
		{"spoofed prefix", "10.0.0.0/24", "10.0.0.1:1234", "192.0.2.8, 203.0.113.1, 10.0.0.2", "https", "203.0.113.1", true},
		{"invalid untrusted prefix ignored", "10.0.0.1", "10.0.0.1:1234", "junk, 203.0.113.1", "https", "203.0.113.1", true},
		{"invalid nearest hop", "10.0.0.1", "10.0.0.1:1234", "203.0.113.1, junk", "http", "10.0.0.1", false},
		{"empty nearest hop", "10.0.0.1", "10.0.0.1:1234", "203.0.113.1,", "http", "10.0.0.1", false},
		{"missing client header", "10.0.0.1", "10.0.0.1:1234", "", "https", "10.0.0.1", true},
		{"ambiguous protocol", "10.0.0.1", "10.0.0.1:1234", "203.0.113.1", "https,http", "203.0.113.1", false},
		{"invalid config rejects all", "10.0.0.1,garbage", "10.0.0.1:1234", "203.0.113.1", "https", "10.0.0.1", false},
		{"empty config component", "10.0.0.1,", "10.0.0.1:1234", "203.0.113.1", "https", "10.0.0.1", false},
		{"IPv6", "2001:db8:1::/64", "[2001:db8:1::1]:1234", "2001:db8:2::5", "https", "2001:db8:2::5", true},
		{"bare IPv6 peer", "", "2001:db8::1", "203.0.113.1", "https", "2001:db8::1", false},
		{"mapped IPv4 peer", "10.0.0.1", "[::ffff:10.0.0.1]:1234", "::ffff:203.0.113.1", "https", "203.0.113.1", true},
		{"mapped IPv4 configuration", "::ffff:10.0.0.0/120", "10.0.0.1:1234", "203.0.113.1", "https", "203.0.113.1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
			r.RemoteAddr = tc.peer
			r.Header.Set("X-Forwarded-For", tc.xff)
			r.Header.Set("X-Forwarded-Proto", tc.proto)
			r.Header.Set("X-Real-IP", "192.0.2.123")
			r.Header.Set("Forwarded", "for=192.0.2.124;proto=https")
			ForwardedHeaders(tc.config)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := ClientIP(r); got != tc.want {
					t.Errorf("client=%q, want %q", got, tc.want)
				}
				if got := IsHTTPS(r); got != tc.secure {
					t.Errorf("HTTPS=%v, want %v", got, tc.secure)
				}
				if r.RemoteAddr != tc.peer {
					t.Errorf("socket peer overwritten: %q", r.RemoteAddr)
				}
			})).ServeHTTP(httptest.NewRecorder(), r)
		})
	}
	r := httptest.NewRequest(http.MethodGet, "https://example.test/", nil)
	r.RemoteAddr = "[2001:db8::5]:1234"
	r.Header.Set("X-Forwarded-For", "192.0.2.1")
	r.TLS = &tls.ConnectionState{}
	if ClientIP(r) != "2001:db8::5" || !IsHTTPS(r) {
		t.Fatal("without proxy middleware, actual peer/TLS must be used")
	}
	// Multiple physical header values are one ordered chain, not alternatives.
	r = httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
	r.RemoteAddr = "10.0.0.1:1234"
	r.Header.Add("X-Forwarded-For", "192.0.2.1")
	r.Header.Add("X-Forwarded-For", "203.0.113.1")
	r.Header.Add("X-Forwarded-Proto", "https")
	r.Header.Add("X-Forwarded-Proto", "http")
	ForwardedHeaders("10.0.0.1")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ClientIP(r) != "203.0.113.1" || IsHTTPS(r) {
			t.Fatalf("duplicate headers: %q", ClientIP(r))
		}
	})).ServeHTTP(httptest.NewRecorder(), r)
}

func TestForwardedContextSurvivesInternalDispatch(t *testing.T) {
	proxy := ForwardedHeaders("10.0.0.1")
	outer := httptest.NewRequest(http.MethodGet, "http://example.test/api/v1/mcp", nil)
	outer.RemoteAddr = "10.0.0.1:1234"
	outer.Header.Set("X-Forwarded-For", "203.0.113.1")
	outer.Header.Set("X-Forwarded-Proto", "https")
	proxy(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// MCP preserves the context and physical transport but builds a new HTTP
		// request without XFF. Re-entering the router must not erase verified data.
		inner := httptest.NewRequestWithContext(r.Context(), http.MethodGet, "http://example.test/api/v1/repos", nil)
		inner.RemoteAddr = r.RemoteAddr
		proxy(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if ClientIP(r) != "203.0.113.1" || !IsHTTPS(r) {
				t.Fatalf("validated transport lost on internal dispatch: IP=%q HTTPS=%v", ClientIP(r), IsHTTPS(r))
			}
		})).ServeHTTP(w, inner)
	})).ServeHTTP(httptest.NewRecorder(), outer)
}
