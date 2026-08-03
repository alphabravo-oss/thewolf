//go:build !fixer_runtime

package scannerbuild

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// The build context contains the Dockerfiles, install inputs, and exact local
// Go source closure needed by scanner and fixer builds. It is embedded so the
// server can build without a checkout.
//
// The files under context/ provide the historical scanner context at the root
// and a repository-shaped tree for fixer builds. To refresh them after editing
// any build input, run `go generate ./internal/scannerbuild/...`.
//
//go:generate go run ./cmd/synccontext
//go:embed all:context
var contextFS embed.FS

// Materialize walks the embedded build context and writes every file to
// dir, preserving relative paths. Shell scripts (*.sh) are written with
// mode 0755 so they're executable inside a docker build; everything else
// is written 0644.
func Materialize(dir string) error {
	return fs.WalkDir(contextFS, "context", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Strip the leading "context/" prefix so files land at the root of dir.
		rel := strings.TrimPrefix(path, "context")
		rel = strings.TrimPrefix(rel, "/")
		if rel == "_wolf-source" {
			return nil
		}
		rel = strings.TrimPrefix(rel, "_wolf-source/")
		switch rel {
		case "wolf-root.go.mod":
			rel = "go.mod"
		case "wolf-root.go.sum":
			rel = "go.sum"
		}
		switch filepath.Base(rel) {
		case "wolf-embedded.go.mod":
			rel = filepath.Join(filepath.Dir(rel), "go.mod")
		case "wolf-embedded.go.sum":
			rel = filepath.Join(filepath.Dir(rel), "go.sum")
		}
		if rel == "" {
			return nil
		}
		target := filepath.Join(dir, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		data, err := contextFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", path, err)
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(rel, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(target, data, mode); err != nil {
			return fmt.Errorf("write %s: %w", target, err)
		}
		return nil
	})
}
