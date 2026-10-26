package httpcache

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDiscoveryRedirectBoundary(t *testing.T) {
	var outsideCalls atomic.Int32
	outside := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		outsideCalls.Add(1)
		_, _ = w.Write([]byte("outside"))
	}))
	defer outside.Close()
	for _, mode := range []string{"same-origin", "cross-port", "userinfo", "method", "loop", "caller-policy"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/done" {
					_, _ = w.Write([]byte("ok"))
					return
				}
				target := "/done"
				switch mode {
				case "cross-port":
					target = outside.URL
				case "userinfo":
					target = "http://user@" + r.Host + "/done"
				case "loop":
					target = "/start"
				}
				http.Redirect(w, r, target, http.StatusFound)
			}))
			defer server.Close()
			client := server.Client()
			var policyCalls int
			if mode == "caller-policy" {
				client.CheckRedirect = func(*http.Request, []*http.Request) error {
					policyCalls++
					return errors.New("caller rejected redirect")
				}
			}
			method := http.MethodGet
			if mode == "method" {
				method = http.MethodPost
			}
			req, err := http.NewRequestWithContext(t.Context(), method, server.URL+"/start", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer sentinel")
			response, err := Do(t.Context(), client, req, Options{MaxBodyBytes: 64})
			if mode == "same-origin" {
				if err != nil || string(response.Body) != "ok" {
					t.Fatalf("same origin: response=%+v err=%v", response, err)
				}
			} else if err == nil {
				t.Fatal("unsafe or caller-rejected redirect succeeded")
			}
			if mode == "caller-policy" && (policyCalls != 1 || !strings.Contains(err.Error(), "caller rejected")) {
				t.Fatalf("caller policy not preserved: calls=%d err=%v", policyCalls, err)
			}
			if mode != "caller-policy" && client.CheckRedirect != nil {
				t.Fatal("caller client was mutated")
			}
		})
	}
	if outsideCalls.Load() != 0 {
		t.Fatal("redirect reached another origin")
	}
}
