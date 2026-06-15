package scannerbuild

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// The build context is ~100 KB of Dockerfiles + install scripts +
// version pins — embedded so the server can build without a checkout.
//
// The files under context/ mirror scanners/ (Dockerfiles, install/,
// versions.env, toolchains.yaml, tools.yaml) at the root, plus the
// autonomous-fix engine Dockerfiles under context/fixer/ (mirroring the
// repo's fixer/ directory). To refresh them after editing scanners/ or
// fixer/, run `go generate ./internal/scannerbuild/...`.
//
//go:generate sh -c "rm -rf context && mkdir -p context/fixer && cp -R ../../scanners/Dockerfile* ../../scanners/install ../../scanners/versions.env ../../scanners/toolchains.yaml ../../scanners/tools.yaml context/ && cp -R ../../fixer/Dockerfile* context/fixer/"
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
