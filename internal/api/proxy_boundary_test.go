package api_test

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/api/routes"
	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestForwardedHeadersCannotBypassAuthenticationRateLimit(t *testing.T) {
	t.Setenv("WOLF_TRUSTED_PROXIES", "")
	srv, _, _ := newTestServer(t)
	for i := 0; i < 6; i++ {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader([]byte(`{}`)))
		r.RemoteAddr = "198.51.100.9:1234"
		r.Header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i+1))
		r.Header.Set("X-Real-IP", fmt.Sprintf("203.0.114.%d", i+1))
		w := httptest.NewRecorder()
		srv.Router.ServeHTTP(w, r)
		want := http.StatusBadRequest
		if i == 5 {
			want = http.StatusTooManyRequests
		}
		if w.Code != want {
			t.Errorf("attempt %d: want %d, got %d: %s", i+1, want, w.Code, w.Body.String())
		}
	}
}

func TestTrustedProxyRateLimitUsesNearestUntrustedHop(t *testing.T) {
	t.Setenv("WOLF_TRUSTED_PROXIES", "10.0.0.1")
	srv, _, _ := newTestServer(t)
	for i := 0; i < 7; i++ {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader([]byte(`{}`)))
		r.RemoteAddr = "10.0.0.1:1234"
		client := "203.0.113.1"
		if i == 6 {
			client = "203.0.113.2"
		}
		r.Header.Set("X-Forwarded-For", fmt.Sprintf("192.0.2.%d, %s", i+1, client))
		w := httptest.NewRecorder()
		srv.Router.ServeHTTP(w, r)
		want := http.StatusBadRequest
		if i == 5 {
			want = http.StatusTooManyRequests
		}
		if w.Code != want {
			t.Errorf("attempt %d: want %d, got %d: %s", i+1, want, w.Code, w.Body.String())
		}
	}
}

func TestSessionCookieUsesTrustedTransport(t *testing.T) {
	for _, tc := range []struct {
		name, config, peer string
		secure             bool
	}{
		{"untrusted", "", "198.51.100.9:1234", false},
		{"loopback untrusted", "", "127.0.0.1:1234", false},
		{"configured proxy", "10.0.0.1", "10.0.0.1:1234", true},
		{"other peer", "10.0.0.1", "10.0.0.2:1234", false},
		{"invalid config", "10.0.0.1,invalid", "10.0.0.1:1234", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WOLF_TRUSTED_PROXIES", tc.config)
			srv, _, _ := newTestServer(t)
			previousAudit := routes.AuditSink
			defer func() { routes.AuditSink = previousAudit }()
			var recorded models.AuditLogEntry
			routes.AuditSink = func(entry models.AuditLogEntry) { recorded = entry }
			r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader([]byte(`{"email":"dev@example.com","password":"password1234"}`)))
			r.RemoteAddr = tc.peer
			r.Header.Set("X-Forwarded-For", "203.0.113.1")
			r.Header.Set("X-Forwarded-Proto", "https")
			w := httptest.NewRecorder()
			srv.Router.ServeHTTP(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("login: %d %s", w.Code, w.Body.String())
			}
			wantIP, _, err := net.SplitHostPort(tc.peer)
			if err != nil {
				t.Fatal(err)
			}
			if tc.secure {
				wantIP = "203.0.113.1"
			}
			if recorded.IP != wantIP || recorded.EventType != "auth.login" {
				t.Fatalf("authentication audit IP=%q event=%q, want %q", recorded.IP, recorded.EventType, wantIP)
			}
			cookies := w.Result().Cookies()
			if len(cookies) != 1 || cookies[0].Name != "wolf_token" || cookies[0].Secure != tc.secure {
				t.Fatalf("unexpected session cookie security: count=%d", len(cookies))
			}
		})
	}
}
