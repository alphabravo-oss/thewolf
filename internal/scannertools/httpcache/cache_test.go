package httpcache

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestDoReusesETagResponseOnNotModified(t *testing.T) {
	var calls atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch calls.Add(1) {
		case 1:
			if request.Header.Get("If-None-Match") != "" {
				t.Fatal("initial request was conditional")
			}
			return cacheResponse(http.StatusOK, `{"version":"1.2.3"}`, map[string]string{
				"ETag": `"release-v1"`,
			}), nil
		case 2:
			if got := request.Header.Get("If-None-Match"); got != `"release-v1"` {
				t.Fatalf("If-None-Match = %q", got)
			}
			return cacheResponse(http.StatusNotModified, "", nil), nil
		default:
			t.Fatalf("unexpected request %d", calls.Load())
			return nil, nil
		}
	})}
	store := NewMemoryStore()
	request := newRequest(t, "https://updates.example/releases")
	first, err := Do(context.Background(), client, request, Options{
		Store: store, MaxBodyBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Do(context.Background(), client, newRequest(t, request.URL.String()), Options{
		Store: store, MaxBodyBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.FromCache || !second.FromCache || string(second.Body) != `{"version":"1.2.3"}` {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
}

func TestDoUsesLastModifiedWhenETagUnavailable(t *testing.T) {
	const modified = "Wed, 30 Jul 2025 12:00:00 GMT"
	var calls atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			return cacheResponse(http.StatusOK, "release-index", map[string]string{
				"Last-Modified": modified,
			}), nil
		}
		if got := request.Header.Get("If-Modified-Since"); got != modified {
			t.Fatalf("If-Modified-Since = %q", got)
		}
		return cacheResponse(http.StatusNotModified, "", nil), nil
	})}
	store := NewMemoryStore()
	if _, err := Do(context.Background(), client, newRequest(t, "https://updates.example/index"), Options{
		Store: store, MaxBodyBytes: 1024,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := Do(context.Background(), client, newRequest(t, "https://updates.example/index"), Options{
		Store: store, MaxBodyBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got.FromCache || string(got.Body) != "release-index" {
		t.Fatalf("response = %+v", got)
	}
}

func TestDoRejectsNotModifiedOnCacheMiss(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return cacheResponse(http.StatusNotModified, "", nil), nil
	})}
	_, err := Do(context.Background(), client, newRequest(t, "https://updates.example/releases"), Options{
		Store: NewMemoryStore(), MaxBodyBytes: 1024,
	})
	if err == nil || !strings.Contains(err.Error(), "without a usable cache entry") {
		t.Fatalf("error = %v", err)
	}
}

func TestDoIgnoresStaleAndIncompatibleEntries(t *testing.T) {
	tests := []struct {
		name  string
		entry func(time.Time, string) Entry
	}{
		{
			name: "stale",
			entry: func(now time.Time, fingerprint string) Entry {
				return Entry{
					SchemaVersion: entrySchemaVersion, Fingerprint: fingerprint,
					StoredAt: now.Add(-8 * 24 * time.Hour), StatusCode: http.StatusOK,
					Header: http.Header{"Etag": {`"stale"`}}, Body: []byte("stale"),
				}
			},
		},
		{
			name: "incompatible schema",
			entry: func(now time.Time, fingerprint string) Entry {
				return Entry{
					SchemaVersion: entrySchemaVersion + 1, Fingerprint: fingerprint,
					StoredAt: now, StatusCode: http.StatusOK,
					Header: http.Header{"Etag": {`"old-schema"`}}, Body: []byte("old"),
				}
			},
		},
		{
			name: "incompatible representation",
			entry: func(now time.Time, _ string) Entry {
				return Entry{
					SchemaVersion: entrySchemaVersion, Fingerprint: "different",
					StoredAt: now, StatusCode: http.StatusOK,
					Header: http.Header{"Etag": {`"wrong"`}}, Body: []byte("wrong"),
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
			request := newRequest(t, "https://updates.example/releases")
			key, fingerprint := RequestKey(request)
			store := NewMemoryStore()
			if err := store.Save(context.Background(), key, test.entry(now, fingerprint)); err != nil {
				t.Fatal(err)
			}
			client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.Header.Get("If-None-Match") != "" ||
					request.Header.Get("If-Modified-Since") != "" {
					t.Fatalf("unusable entry produced conditional request: %#v", request.Header)
				}
				return cacheResponse(http.StatusOK, "fresh", map[string]string{
					"ETag": `"fresh"`,
				}), nil
			})}
			got, err := Do(context.Background(), client, request, Options{
				Store: store, MaxBodyBytes: 1024, Now: func() time.Time { return now },
			})
			if err != nil {
				t.Fatal(err)
			}
			if got.FromCache || string(got.Body) != "fresh" {
				t.Fatalf("response = %+v", got)
			}
		})
	}
}

func TestDoNormalResponseWithoutValidatorIsNotCached(t *testing.T) {
	var calls atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		if request.Header.Get("If-None-Match") != "" ||
			request.Header.Get("If-Modified-Since") != "" {
			t.Fatal("response without validators was cached")
		}
		return cacheResponse(http.StatusOK, "normal", nil), nil
	})}
	store := NewMemoryStore()
	for range 2 {
		got, err := Do(context.Background(), client, newRequest(t, "https://updates.example/releases"), Options{
			Store: store, MaxBodyBytes: 1024,
		})
		if err != nil || string(got.Body) != "normal" {
			t.Fatalf("response=%+v err=%v", got, err)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d", calls.Load())
	}
}

func newRequest(t *testing.T, rawURL string) *http.Request {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Accept", "application/json")
	return request
}

func cacheResponse(status int, body string, headers map[string]string) *http.Response {
	response := &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	for name, value := range headers {
		response.Header.Set(name, value)
	}
	return response
}
