package httpcache

import (
	"context"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"testing"
)

func TestCacheDoesNotSharePrivateOrNegotiatedResponses(t *testing.T) {
	for _, name := range []string{"authorization", "cookie", "cookie-jar", "no-store-request", "no-store", "private", "vary-language", "vary-star", "set-cookie", "post"} {
		t.Run(name, func(t *testing.T) {
			store := NewMemoryStore()
			request := newRequest(t, "https://updates.example/releases")
			headers := map[string]string{"ETag": `"shared-validator"`}
			switch name {
			case "authorization":
				request.Header.Set("Authorization", "Bearer private")
			case "cookie":
				request.Header.Set("Cookie", "session=private")
			case "no-store-request":
				request.Header.Set("Cache-Control", "no-store")
			case "no-store", "private":
				headers["Cache-Control"] = name
			case "vary-language":
				headers["Vary"] = "Accept-Language"
			case "vary-star":
				headers["Vary"] = "*"
			case "set-cookie":
				headers["Set-Cookie"] = "session=private"
			case "post":
				request.Method = http.MethodPost
			}
			client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.Header.Get("If-None-Match") != "" {
					t.Error("private or negotiated response was reused")
				}
				return cacheResponse(http.StatusOK, "private response", headers), nil
			})}
			if name == "cookie-jar" {
				client.Jar, _ = cookiejar.New(nil)
			}
			for i := 0; i < 2; i++ {
				if _, err := Do(t.Context(), client, request, Options{Store: store, MaxBodyBytes: 1024}); err != nil {
					t.Fatal(err)
				}
			}
			key, _ := RequestKey(request)
			if _, present, err := store.Load(t.Context(), key); err != nil || present {
				t.Fatalf("private response stored: present=%v error=%v", present, err)
			}
		})
	}
}

func TestCacheRequestOwnershipAndCancellation(t *testing.T) {
	store := NewMemoryStore()
	request := newRequest(t, "https://updates.example/releases")
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if err := request.Context().Err(); err != nil {
			return nil, err
		}
		calls++
		if calls == 1 {
			return cacheResponse(http.StatusOK, "index", map[string]string{"ETag": `"v1"`}), nil
		}
		if request.Header.Get("If-None-Match") != `"v1"` {
			t.Error("missing validator")
		}
		return cacheResponse(http.StatusNotModified, "", map[string]string{"Cache-Control": "no-store"}), nil
	})}
	for i := 0; i < 2; i++ {
		if _, err := Do(t.Context(), client, request, Options{Store: store, MaxBodyBytes: 1024}); err != nil {
			t.Fatal(err)
		}
	}
	if request.Header.Get("If-None-Match") != "" {
		t.Fatal("conditional header leaked into caller-owned request")
	}
	key, _ := RequestKey(request)
	if _, present, _ := store.Load(t.Context(), key); present {
		t.Fatal("304 no-store failed to evict prior representation")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Do(ctx, client, request, Options{MaxBodyBytes: 1024}); !errors.Is(err, context.Canceled) {
		t.Fatalf("caller cancellation ignored: %v", err)
	}
}
