package latest

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDockerPaginationConfinesBearerToken(t *testing.T) {
	var outsideCalls atomic.Int32
	outside := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		outsideCalls.Add(1)
	}))
	defer outside.Close()
	for _, mode := range []string{"relative", "cross-port", "userinfo", "scheme"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer sentinel" {
					t.Error("missing registry token")
				}
				next := "/v2/tool/tags/list?last=one"
				switch mode {
				case "cross-port":
					next = outside.URL + "/tags"
				case "userinfo":
					next = "http://user@" + r.Host + next
				case "scheme":
					next = "https://" + r.Host + next
				}
				w.Header().Set("Link", fmt.Sprintf(`<%s>; rel="next"`, next))
				_, _ = w.Write([]byte(`{"tags":["1.0.0"]}`))
			}))
			defer server.Close()
			checker := Checker{Client: server.Client()}
			_, next, _, err := checker.getDockerTagsPage(t.Context(), server.URL+"/tags", strings.TrimPrefix(server.URL, "http://"), "tool", "sentinel")
			if mode == "relative" {
				if err != nil || next != server.URL+"/v2/tool/tags/list?last=one" {
					t.Fatalf("relative pagination: next=%q err=%v", next, err)
				}
			} else if err == nil || next != "" {
				t.Fatalf("unsafe pagination accepted: next=%q err=%v", next, err)
			}
		})
	}
	if outsideCalls.Load() != 0 {
		t.Fatal("pagination reached another origin")
	}
}
