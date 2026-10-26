package main

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSourceIntegrityDownload(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/missing":
			w.WriteHeader(http.StatusNotFound)
		case "/redirect":
			http.Redirect(w, r, "http://example.com/plaintext", http.StatusFound)
		default:
			_, _ = w.Write([]byte("verified archive"))
		}
	}))
	defer server.Close()
	got, err := sourceSHA256(server.Client(), server.URL)
	want := fmt.Sprintf("%x", sha256.Sum256([]byte("verified archive")))
	if err != nil || got != want {
		t.Fatalf("hash = %q, %v; want %s", got, err, want)
	}
	for _, path := range []string{"/missing", "/redirect"} {
		if _, err := sourceSHA256(server.Client(), server.URL+path); err == nil {
			t.Fatalf("accepted %s", path)
		}
	}
}
