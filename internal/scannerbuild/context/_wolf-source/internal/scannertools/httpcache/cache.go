// Package httpcache provides bounded RFC 9110 conditional-request caching for
// scanner discovery metadata. Cache entries are keyed by a deterministic
// request representation and never include credentials.
package httpcache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	entrySchemaVersion = 1
	defaultMaxAge      = 7 * 24 * time.Hour
	maxValidatorLength = 1024
)

var ErrNotModifiedWithoutUsableCache = errors.New("remote source returned 304 without a usable cache entry")

type Entry struct {
	SchemaVersion int
	Fingerprint   string
	StoredAt      time.Time
	StatusCode    int
	Header        http.Header
	Body          []byte
}

type Store interface {
	Load(context.Context, string) (Entry, bool, error)
	Save(context.Context, string, Entry) error
	Delete(context.Context, string) error
}

type MemoryStore struct {
	mu      sync.RWMutex
	entries map[string]Entry
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{entries: map[string]Entry{}}
}

func (s *MemoryStore) Load(_ context.Context, key string) (Entry, bool, error) {
	if s == nil {
		return Entry{}, false, nil
	}
	s.mu.RLock()
	entry, ok := s.entries[key]
	s.mu.RUnlock()
	if !ok {
		return Entry{}, false, nil
	}
	return cloneEntry(entry), true, nil
}

func (s *MemoryStore) Save(_ context.Context, key string, entry Entry) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.entries == nil {
		s.entries = map[string]Entry{}
	}
	s.entries[key] = cloneEntry(entry)
	s.mu.Unlock()
	return nil
}

func (s *MemoryStore) Delete(_ context.Context, key string) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	delete(s.entries, key)
	s.mu.Unlock()
	return nil
}

type Options struct {
	Store        Store
	MaxBodyBytes int64
	MaxAge       time.Duration
	Now          func() time.Time
}

type Response struct {
	StatusCode int
	Status     string
	Header     http.Header
	Body       []byte
	FromCache  bool
}

// Do executes req through client. A fresh, compatible cached representation
// adds conditional headers. A 304 is converted into the cached successful
// response; a 304 without that representation is an explicit error.
func Do(ctx context.Context, client *http.Client, req *http.Request, opts Options) (Response, error) {
	if req == nil || req.URL == nil {
		return Response{}, errors.New("conditional request is nil")
	}
	if client == nil {
		client = http.DefaultClient
	}
	if opts.MaxBodyBytes < 0 {
		return Response{}, errors.New("conditional request body limit must not be negative")
	}
	now := time.Now().UTC()
	if opts.Now != nil {
		now = opts.Now().UTC()
	}
	maxAge := opts.MaxAge
	if maxAge <= 0 {
		maxAge = defaultMaxAge
	}
	key, fingerprint := RequestKey(req)
	cached, usable := loadUsable(ctx, opts.Store, key, fingerprint, now, maxAge, opts.MaxBodyBytes)
	if usable {
		if etag := cached.Header.Get("ETag"); validETag(etag) {
			req.Header.Set("If-None-Match", etag)
		}
		if modified := cached.Header.Get("Last-Modified"); validLastModified(modified) {
			req.Header.Set("If-Modified-Since", modified)
		}
	}

	response, err := client.Do(req)
	if err != nil {
		return Response{}, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotModified {
		if !usable {
			return Response{}, ErrNotModifiedWithoutUsableCache
		}
		cached.StoredAt = now
		mergeCacheHeaders(cached.Header, response.Header)
		_ = opts.Store.Save(ctx, key, cached)
		return responseFromEntry(cached, true), nil
	}

	result := Response{
		StatusCode: response.StatusCode,
		Status:     response.Status,
		Header:     cloneHeaders(response.Header),
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return result, nil
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, opts.MaxBodyBytes+1))
	if err != nil {
		return Response{}, err
	}
	if int64(len(body)) > opts.MaxBodyBytes {
		return Response{}, fmt.Errorf("remote source response exceeds %d-byte limit", opts.MaxBodyBytes)
	}
	result.Body = body
	if opts.Store == nil {
		return result, nil
	}
	cacheHeaders := selectedCacheHeaders(response.Header)
	if !hasValidator(cacheHeaders) {
		_ = opts.Store.Delete(ctx, key)
		return result, nil
	}
	entry := Entry{
		SchemaVersion: entrySchemaVersion,
		Fingerprint:   fingerprint,
		StoredAt:      now,
		StatusCode:    response.StatusCode,
		Header:        cacheHeaders,
		Body:          append([]byte(nil), body...),
	}
	_ = opts.Store.Save(ctx, key, entry)
	return result, nil
}

func RequestKey(req *http.Request) (key, fingerprint string) {
	accept := strings.Join(req.Header.Values("Accept"), ",")
	fingerprint = strings.Join([]string{
		"http-discovery-cache/v1",
		strings.ToUpper(req.Method),
		req.URL.String(),
		accept,
	}, "\n")
	sum := sha256.Sum256([]byte(fingerprint))
	return "sha256:" + hex.EncodeToString(sum[:]), fingerprint
}

func loadUsable(
	ctx context.Context,
	store Store,
	key, fingerprint string,
	now time.Time,
	maxAge time.Duration,
	maxBodyBytes int64,
) (Entry, bool) {
	if store == nil {
		return Entry{}, false
	}
	entry, ok, err := store.Load(ctx, key)
	if err != nil || !ok {
		return Entry{}, false
	}
	future := entry.StoredAt.After(now.Add(5 * time.Minute))
	stale := entry.StoredAt.IsZero() || now.Sub(entry.StoredAt) > maxAge
	incompatible := entry.SchemaVersion != entrySchemaVersion ||
		entry.Fingerprint != fingerprint ||
		entry.StatusCode < http.StatusOK ||
		entry.StatusCode >= http.StatusMultipleChoices ||
		int64(len(entry.Body)) > maxBodyBytes ||
		!hasValidator(entry.Header)
	if future || stale || incompatible {
		_ = store.Delete(ctx, key)
		return Entry{}, false
	}
	return cloneEntry(entry), true
}

func responseFromEntry(entry Entry, fromCache bool) Response {
	return Response{
		StatusCode: entry.StatusCode,
		Status:     fmt.Sprintf("%d %s", entry.StatusCode, http.StatusText(entry.StatusCode)),
		Header:     cloneHeaders(entry.Header),
		Body:       append([]byte(nil), entry.Body...),
		FromCache:  fromCache,
	}
}

func selectedCacheHeaders(headers http.Header) http.Header {
	selected := make(http.Header)
	for _, name := range []string{
		"ETag",
		"Last-Modified",
		"Content-Type",
		"Docker-Content-Digest",
		"Link",
	} {
		for _, value := range headers.Values(name) {
			selected.Add(name, value)
		}
	}
	if etag := selected.Get("ETag"); etag != "" && !validETag(etag) {
		selected.Del("ETag")
	}
	if modified := selected.Get("Last-Modified"); modified != "" && !validLastModified(modified) {
		selected.Del("Last-Modified")
	}
	return selected
}

func mergeCacheHeaders(cached, revalidated http.Header) {
	for _, name := range []string{
		"ETag",
		"Last-Modified",
		"Content-Type",
		"Docker-Content-Digest",
		"Link",
	} {
		values := revalidated.Values(name)
		if len(values) == 0 {
			continue
		}
		cached.Del(name)
		for _, value := range values {
			cached.Add(name, value)
		}
	}
	if etag := cached.Get("ETag"); etag != "" && !validETag(etag) {
		cached.Del("ETag")
	}
	if modified := cached.Get("Last-Modified"); modified != "" && !validLastModified(modified) {
		cached.Del("Last-Modified")
	}
}

func hasValidator(headers http.Header) bool {
	return validETag(headers.Get("ETag")) || validLastModified(headers.Get("Last-Modified"))
}

func validETag(value string) bool {
	if value == "" || len(value) > maxValidatorLength {
		return false
	}
	if strings.HasPrefix(value, "W/") {
		value = strings.TrimPrefix(value, "W/")
	}
	return len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' &&
		!strings.ContainsAny(value[1:len(value)-1], "\r\n")
}

func validLastModified(value string) bool {
	if value == "" || len(value) > maxValidatorLength {
		return false
	}
	_, err := http.ParseTime(value)
	return err == nil
}

func cloneEntry(entry Entry) Entry {
	entry.Header = cloneHeaders(entry.Header)
	entry.Body = append([]byte(nil), entry.Body...)
	return entry
}

func cloneHeaders(headers http.Header) http.Header {
	if headers == nil {
		return make(http.Header)
	}
	return headers.Clone()
}
