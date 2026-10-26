package api

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/wolflog"
)

// MountStaticUI attaches a SPA file-server to the router for the Wolf v2 UI.
//
// Behavior:
//   - Files inside `dir` are served verbatim (HTML/CSS/JS/images, with
//     content-type inferred by http.ServeFile).
//   - Unknown non-/api paths fall back to `index.html` so client-side
//     routing (TanStack Router) works for any deep link.
//   - If `dir` is empty or doesn't exist, the SPA handler is a no-op
//     and the route falls through to the 404 chain — useful in
//     `--api-only` deployments or when running the UI separately
//     via `npm run dev`.
//
// Mount this after the /api router so /api/* always takes precedence:
//
//	api.MountStaticUI(r, os.Getenv("WOLF_UI_DIR"))
//
// The handler is attached via NotFound rather than a `/*` catch-all because
// the router already has a `/api/*` deprecating-alias wildcard, and chi's
// trie can't have two overlapping root-level wildcards — registering both
// silently disables the SPA route.
func MountStaticUI(mount interface {
	NotFound(h http.HandlerFunc)
}, dir string,
) {
	if dir == "" {
		// Default search path: try a few common locations so operators
		// don't have to set WOLF_UI_DIR explicitly.
		for _, candidate := range []string{
			"/usr/share/wolf/ui/dist",
			"./ui/dist",
			"./dist",
		} {
			if st, err := os.Stat(candidate); err == nil && st.IsDir() {
				dir = candidate
				break
			}
		}
	}
	if dir == "" {
		wolflog.Info().Msg("static-ui: no dist dir found; UI routes disabled (run `npm run dev` separately)")
		return
	}

	indexPath := filepath.Join(dir, "index.html")
	if _, err := os.Stat(indexPath); err != nil { // #nosec G703 -- dir is operator WOLF_UI_DIR configuration or a compiled default; this startup probe has no HTTP input.
		wolflog.Warn().Str("dir", dir).Err(err).Msg("static-ui: index.html missing; SPA fallback disabled")
		return
	}

	dir = filepath.Clean(dir)

	wolflog.Info().Str("dir", dir).Msg("static-ui: serving SPA")

	handler := spaHandler{dir: dir}
	// NotFound runs whenever no /api/* route matches — exactly the right
	// hook for an SPA fallback. Works for GET and HEAD (and any verb the
	// SPA may need for asset preflight).
	mount.NotFound(handler.serve)
}

type spaHandler struct {
	dir string
}

// serve dispatches a request to the static file if it exists, otherwise
// falls back to index.html. Never 404s for HTML routes — that's the SPA
// contract.
func (h spaHandler) serve(w http.ResponseWriter, r *http.Request) {
	// Anything under /api/* should never reach here because the api router
	// mounts first, but defend in depth.
	if strings.HasPrefix(r.URL.Path, "/api/") {
		http.NotFound(w, r)
		return
	}

	urlPath := strings.TrimPrefix(r.URL.Path, "/")
	if urlPath == "" {
		urlPath = "index.html"
	}

	if !fs.ValidPath(urlPath) {
		http.NotFound(w, r)
		return
	}
	root, err := os.OpenRoot(h.dir)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer func() { _ = root.Close() }()
	// Root confines both the metadata lookup and the subsequent file open,
	// including symlinks swapped between those operations.
	if st, err := root.Stat(urlPath); err == nil && st.Mode().IsRegular() {
		if strings.HasPrefix(urlPath, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		http.ServeFileFS(w, r, root.FS(), urlPath) // #nosec G703 -- fs.ValidPath rejects traversal and root.FS confines all file opens, including symlink resolution.
		return
	}

	// SPA fallback.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFileFS(w, r, root.FS(), "index.html")
}
