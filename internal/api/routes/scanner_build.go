package routes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/scannerbuild"
	"github.com/alphabravocompany/thewolf/internal/secrets"
	"github.com/alphabravocompany/thewolf/internal/setup/scanners"
)

// defaultScannerNamespace is the DockerHub namespace used when the
// scanner_registry_namespace setting is unset. Mirrors the runtime default.
const defaultScannerNamespace = "alphabravodevops"

// ScannerBuildFn is the indirection point for the docker buildx runner. It is
// an exported package var so tests can stub it and never invoke a real docker
// daemon. Defaults to scannerbuild.Build.
var ScannerBuildFn = scannerbuild.Build

// buildScannerBody is the request payload for both build endpoints.
//   - push: publish to the registry (needs a dockerhub_token secret). Default
//     false → local --load build.
//   - multi_arch: build linux/amd64+arm64 via buildx. Implies push (a manifest
//     list can't be --load'ed locally) and needs a QEMU buildx builder.
//   - platforms: optional explicit override (e.g. "linux/amd64").
type buildScannerBody struct {
	Push      bool   `json:"push"`
	MultiArch bool   `json:"multi_arch"`
	Platforms string `json:"platforms,omitempty"`
}

// scannerImageVersionSetting persists the current published scanner-image
// semver. Each push (publish) build bumps its patch so every published rebuild
// is a distinct immutable tag.
const scannerImageVersionSetting = "scanners_image_version"

// defaultMultiArchPlatforms is what a multi_arch build targets when no explicit
// platforms override is given.
const defaultMultiArchPlatforms = "linux/amd64,linux/arm64"

// BuildScannerImage builds one wolf-built scanner variant from the embedded
// context and streams docker buildx output over SSE.
//
// Body: {"push": bool} (default false). A push=false build loads the image
// into the local daemon and needs no credentials. A push=true build requires
// a dockerhub_token secret (404 with a hint if absent).
//
// Route: POST /api/v1/scanners/images/{variant}/build
func BuildScannerImage(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	variant := chi.URLParam(r, "variant")
	if _, ok := scannerbuild.VariantByName(variant); !ok {
		response.WriteError(w, http.StatusNotFound, "not_found",
			fmt.Sprintf("unknown scanner variant %q (want one of default|jvm|rust|codeql)", variant))
		return
	}

	push, platforms := decodeBuildOpts(r)
	version := resolveBuildVersion(r.Context(), h, push)

	req, ok := buildRequestFor(w, r, h, claims.UserID, variant, push, version, platforms)
	if !ok {
		return
	}

	flusher, ok := beginSSE(w)
	if !ok {
		return
	}
	streamOneBuild(r.Context(), w, flusher, req, "")
}

// BuildAllScannerImages builds all four variants in sequence, prefixing each
// streamed line with [variant]. Same push semantics as BuildScannerImage.
//
// Route: POST /api/v1/scanners/images/build-all
func BuildAllScannerImages(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	push, platforms := decodeBuildOpts(r)

	// Resolve creds once up front so a missing dockerhub_token on a push
	// build 404s before we open the SSE stream.
	user, pat := "", ""
	if push {
		var ok bool
		user, pat, ok = resolveDockerHubCreds(w, r, h, claims.UserID)
		if !ok {
			return
		}
	}
	namespace := resolveScannerNamespace(r.Context(), h)
	// One version bump for the whole build-all run, so all four variants share
	// the same new immutable tag.
	version := resolveBuildVersion(r.Context(), h, push)

	flusher, ok := beginSSE(w)
	if !ok {
		return
	}
	for _, v := range scannerbuild.Variants {
		req := scannerbuild.BuildRequest{
			Variant:       v.Name,
			Namespace:     namespace,
			Version:       version,
			Push:          push,
			DockerHubUser: user,
			DockerHubPAT:  pat,
			Platforms:     platforms,
		}
		if !streamOneBuild(r.Context(), w, flusher, req, v.Name) {
			return
		}
	}
}

// decodeBuildOpts reads {push, multi_arch, platforms}, defaulting to a local
// single-arch build. A multi-arch build implies push (a manifest list can't be
// --load'ed locally). Returns the effective push flag and the platforms spec
// ("" = single-arch native).
func decodeBuildOpts(r *http.Request) (push bool, platforms string) {
	if r.Body == nil {
		return false, ""
	}
	var body buildScannerBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return false, ""
	}
	platforms = strings.TrimSpace(body.Platforms)
	if body.MultiArch && platforms == "" {
		platforms = defaultMultiArchPlatforms
	}
	push = body.Push
	if scannerbuild.IsMultiPlatform(platforms) {
		push = true // multi-arch must be pushed
	}
	return push, platforms
}

// resolveBuildVersion returns the image version to tag a build with. A push
// (publish) build auto-increments the persisted scanner-image semver patch and
// saves it, so every published rebuild is a distinct immutable tag. A local
// (--load) build reuses the current version without bumping.
func resolveBuildVersion(ctx context.Context, h *Handler, push bool) string {
	cur := strings.TrimSpace(getSettingOr(ctx, h, scannerImageVersionSetting, scanners.DefaultScannersTag))
	if cur == "" {
		cur = scanners.DefaultScannersTag
	}
	if !push {
		return cur
	}
	next := scannerbuild.BumpPatch(cur)
	_ = h.Store.SetSetting(ctx, scannerImageVersionSetting, next)
	return next
}

// getSettingOr returns the setting value or a fallback when unset/errored.
func getSettingOr(ctx context.Context, h *Handler, key, fallback string) string {
	if v, err := h.Store.GetSetting(ctx, key); err == nil && strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}

// buildRequestFor assembles a BuildRequest, resolving namespace + creds (only
// when push is set). version + platforms are resolved by the caller. On a
// missing push credential it writes the 404 and returns ok=false.
func buildRequestFor(w http.ResponseWriter, r *http.Request, h *Handler, userID, variant string, push bool, version, platforms string) (scannerbuild.BuildRequest, bool) {
	req := scannerbuild.BuildRequest{
		Variant:   variant,
		Namespace: resolveScannerNamespace(r.Context(), h),
		Version:   version,
		Push:      push,
		Platforms: platforms,
	}
	if push {
		user, pat, ok := resolveDockerHubCreds(w, r, h, userID)
		if !ok {
			return scannerbuild.BuildRequest{}, false
		}
		req.DockerHubUser = user
		req.DockerHubPAT = pat
	}
	return req, true
}

// resolveScannerNamespace reads the scanner_registry_namespace setting,
// falling back to the DockerHub default.
func resolveScannerNamespace(ctx context.Context, h *Handler) string {
	if h != nil && h.Store != nil {
		if ns, err := h.Store.GetSetting(ctx, "scanner_registry_namespace"); err == nil {
			if ns = strings.TrimSpace(ns); ns != "" {
				return ns
			}
		}
	}
	return defaultScannerNamespace
}

// resolveDockerHubCreds loads the first dockerhub_token secret and returns the
// username (key_name) + decrypted PAT. When no such secret exists it writes a
// 404 with an actionable hint and returns ok=false. Only called on push.
func resolveDockerHubCreds(w http.ResponseWriter, r *http.Request, h *Handler, userID string) (user, pat string, ok bool) {
	secs, err := h.Store.ListSecretsByUser(r.Context(), userID)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list secrets")
		return "", "", false
	}
	var chosen *models.Secret
	for i := range secs {
		if secs[i].KeyType == models.KeyTypeDockerHubToken {
			chosen = &secs[i]
			break
		}
	}
	if chosen == nil {
		response.WriteError(w, http.StatusNotFound, "dockerhub_token_missing",
			"no dockerhub_token secret configured — add a DockerHub username + PAT to publish, or build with push:false to load locally")
		return "", "", false
	}
	pat, err = secrets.Decrypt(chosen.EncryptedValue)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to decrypt dockerhub_token secret")
		return "", "", false
	}
	return chosen.KeyName, pat, true
}

// beginSSE writes the event-stream headers and returns the flusher, or writes
// a 500 if the writer can't stream.
func beginSSE(w http.ResponseWriter) (http.Flusher, bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "streaming not supported")
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	return flusher, true
}

// streamOneBuild runs a single build, streaming each line as an SSE `data:`
// frame and ending with a terminal `event: done` or `event: error` frame.
// When prefix is non-empty each line is tagged "[prefix] ". Returns false when
// the build failed so build-all can stop the stream.
func streamOneBuild(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, req scannerbuild.BuildRequest, prefix string) bool {
	tag := ""
	if prefix != "" {
		tag = "[" + prefix + "] "
	}
	onLine := func(line string) {
		writeSSEData(w, flusher, tag+line)
	}
	res, err := ScannerBuildFn(ctx, req, onLine)
	if err != nil {
		writeSSEEvent(w, flusher, "error", map[string]string{"variant": req.Variant, "error": err.Error()})
		return false
	}
	writeSSEEvent(w, flusher, "done", map[string]any{
		"variant":        req.Variant,
		"refs":           res.Refs,
		"digest":         res.Digest,
		"loaded_locally": res.LoadedLocally,
		"pushed":         req.Push,
	})
	return true
}

// writeSSEData emits one `data:` frame and flushes.
func writeSSEData(w http.ResponseWriter, flusher http.Flusher, line string) {
	fmt.Fprintf(w, "data: %s\n\n", line) // nosemgrep: go.lang.security.audit.xss.no-fprintf-to-responsewriter.no-fprintf-to-responsewriter
	flusher.Flush()
}

// writeSSEEvent emits a named SSE event whose data is JSON, then flushes.
func writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, event string, payload any) {
	b, _ := json.Marshal(payload)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, string(b)) // nosemgrep: go.lang.security.audit.xss.no-fprintf-to-responsewriter.no-fprintf-to-responsewriter
	flusher.Flush()
}
