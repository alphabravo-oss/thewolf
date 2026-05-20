// Package openapi builds and serves the wolf API's OpenAPI 3 specification
// and the interactive documentation UIs.
package openapi

import (
	"embed"
	"io/fs"
)

// assetsFS holds the Swagger UI and ReDoc bundles, vendored into the binary
// so the documentation pages render fully offline — no CDN, no internet.
//
//go:embed assets/swagger-ui.css assets/swagger-ui-bundle.js assets/redoc.standalone.js
var assetsFS embed.FS

// AssetsSub returns the embedded UI assets rooted so each file is served
// directly (e.g. "swagger-ui.css"), suitable for an http.FileServer.
func AssetsSub() fs.FS {
	sub, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		// The embed directive is static; this cannot fail at runtime.
		panic(err)
	}
	return sub
}

// SwaggerUIHTML is a self-contained Swagger UI page. It loads the spec from
// /api/v1/openapi.json and its CSS/JS from the locally-served, embedded
// assets — so the page works with no external network access.
const SwaggerUIHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>wolf API — Swagger UI</title>
  <link rel="stylesheet" href="/api/v1/docs/static/swagger-ui.css">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="/api/v1/docs/static/swagger-ui-bundle.js"></script>
  <script>
    window.ui = SwaggerUIBundle({
      url: "/api/v1/openapi.json",
      dom_id: "#swagger-ui",
      deepLinking: true,
      persistAuthorization: true
    });
  </script>
</body>
</html>`

// RedocHTML renders the same spec with ReDoc from the embedded bundle.
const RedocHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>wolf API — Reference</title>
  <meta name="viewport" content="width=device-width, initial-scale=1">
</head>
<body>
  <redoc spec-url="/api/v1/openapi.json"></redoc>
  <script src="/api/v1/docs/static/redoc.standalone.js"></script>
</body>
</html>`
