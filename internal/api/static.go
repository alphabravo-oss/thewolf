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
func MountStaticUI(mount interface {
	Get(pattern string, h http.HandlerFunc)
	Head(pattern string, h http.HandlerFunc)
}, dir string,
) {
	if dir == "" {
		// Default search path: try a few common locations so operators
		// don't have to set WOLF_UI_DIR explicitly.
		for _, candidate := range []string{
			"/usr/share/wolf/ui/dist",
			"./ui-next/dist",
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

	root := os.DirFS(dir)
	indexPath := filepath.Join(dir, "index.html")
	if _, err := os.Stat(indexPath); err != nil {
		wolflog.Warn().Str("dir", dir).Err(err).Msg("static-ui: index.html missing; SPA fallback disabled")
		return
	}

	wolflog.Info().Str("dir", dir).Msg("static-ui: serving SPA")

	handler := spaHandler{dir: dir, root: root, indexPath: indexPath}
	// Wildcard route so EVERY non-/api path lands here. GET handles
	// normal navigation + asset loads; HEAD lets browsers (and curl -I)
	// preflight cacheable assets without paying the body cost.
	mount.Get("/*", handler.serve)
	mount.Head("/*", handler.serve)
}

type spaHandler struct {
	dir       string
	root      fs.FS
	indexPath string
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

	// Resolve the requested path inside dir. If it points to a regular file,
	// serve it. Otherwise fall back to index.html.
	candidate := filepath.Join(h.dir, urlPath)
	if !strings.HasPrefix(candidate, h.dir) {
		// Path-traversal guard.
		http.NotFound(w, r)
		return
	}
	if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
		// Cache hashed JS/CSS assets aggressively; everything else is short.
		if strings.HasPrefix(urlPath, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		http.ServeFile(w, r, candidate)
		return
	}

	// SPA fallback.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, h.indexPath)
}
