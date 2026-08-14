package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

type listedPackage struct {
	Dir          string
	GoFiles      []string
	CgoFiles     []string
	CFiles       []string
	CXXFiles     []string
	MFiles       []string
	HFiles       []string
	FFiles       []string
	SFiles       []string
	SwigFiles    []string
	SwigCXXFiles []string
	SysoFiles    []string
	EmbedFiles   []string
}

func main() {
	check := flag.Bool("check", false, "fail if the committed embedded context differs from canonical sources")
	rootFlag := flag.String("root", "", "repository root (defaults to the source checkout containing this command)")
	flag.Parse()
	root := strings.TrimSpace(*rootFlag)
	if root == "" {
		_, source, _, ok := runtime.Caller(0)
		if !ok {
			panic("resolve generator source path")
		}
		root = filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", "..", ".."))
	} else {
		if !filepath.IsAbs(root) || filepath.Clean(root) != root {
			panic("repository root must be an absolute clean path")
		}
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			panic("repository root is not a directory")
		}
	}
	committed := filepath.Join(root, "internal", "scannerbuild", "context")
	destination := committed
	if *check {
		temporary, err := os.MkdirTemp("", "wolf-scanner-context-check-*")
		if err != nil {
			panic(err)
		}
		defer os.RemoveAll(temporary)
		destination = filepath.Join(temporary, "context")
	}
	if err := os.RemoveAll(destination); err != nil {
		panic(err)
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		panic(err)
	}

	for _, relative := range []string{".dockerignore"} {
		mustCopy(root, destination, relative)
	}
	// Keep compiled Wolf sources under a Go-ignored directory in the committed
	// generated tree. Materialize strips this prefix back to the repository
	// root. Without the encoding, `go test ./...` would discover and compile a
	// second copy of every embedded package.
	sourceDestination := filepath.Join(destination, "_wolf-source")
	mustCopyAs(root, sourceDestination, "go.mod", "wolf-root.go.mod")
	mustCopyAs(root, sourceDestination, "go.sum", "wolf-root.go.sum")
	for _, relative := range []string{
		"scanners/Dockerfile",
		"scanners/Dockerfile.codeql",
		"scanners/Dockerfile.jvm",
		"scanners/Dockerfile.min",
		"scanners/Dockerfile.rust",
		"scanners/go-tools",
		"scanners/install",
		"scanners/os-packages",
		"scanners/os-packages.lock.yaml",
		"scanners/os-packages.yaml",
		"scanners/smoke-test.sh",
		"scanners/toolchains.yaml",
		"scanners/tools.yaml",
		"scanners/trufflehog-excludes.txt",
		"scanners/versions.env",
		"scanners/wolf-tool-entry",
	} {
		target := strings.TrimPrefix(relative, "scanners/")
		mustCopyAs(root, destination, relative, target)
		mustCopy(root, destination, relative)
	}
	for _, relative := range []string{
		"fixer/Dockerfile.api",
		"fixer/Dockerfile.base",
		"fixer/Dockerfile.claude",
		"fixer/Dockerfile.codex",
		"fixer/go-tools",
		"fixer/install-node-tools.sh",
		"fixer/install-fix-tools.sh",
		"fixer/versions.env",
	} {
		mustCopy(root, destination, relative)
	}
	if err := copyFixerBuildSources(root, sourceDestination); err != nil {
		panic(err)
	}
	if *check {
		if err := compareTrees(committed, destination); err != nil {
			panic(fmt.Errorf("embedded scanner build context is stale; run go generate ./internal/scannerbuild/...: %w", err))
		}
	}
}

func compareTrees(left, right string) error {
	leftFiles, err := treeFiles(left)
	if err != nil {
		return err
	}
	rightFiles, err := treeFiles(right)
	if err != nil {
		return err
	}
	if len(leftFiles) != len(rightFiles) {
		return fmt.Errorf("file count %d does not match generated count %d", len(leftFiles), len(rightFiles))
	}
	for index, relative := range leftFiles {
		if rightFiles[index] != relative {
			return fmt.Errorf("file set differs at %q and %q", relative, rightFiles[index])
		}
		leftData, err := os.ReadFile(filepath.Join(left, relative))
		if err != nil {
			return err
		}
		rightData, err := os.ReadFile(filepath.Join(right, relative))
		if err != nil {
			return err
		}
		if !bytes.Equal(leftData, rightData) {
			return fmt.Errorf("content differs for %s", relative)
		}
	}
	return nil
}

func treeFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, relative)
		return nil
	})
	sort.Strings(files)
	return files, err
}

func copyFixerBuildSources(root, destination string) error {
	files := make(map[string]struct{})
	for _, architecture := range []string{"amd64", "arm64"} {
		command := exec.Command(
			"go", "list", "-deps", "-json",
			"-tags", "fixer_runtime sqlite_omit_load_extension netgo osusergo",
			"./cmd/wolf",
		)
		command.Dir = root
		command.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+architecture, "CGO_ENABLED=1")
		output, err := command.StdoutPipe()
		if err != nil {
			return err
		}
		command.Stderr = os.Stderr
		if err := command.Start(); err != nil {
			return err
		}
		decoder := json.NewDecoder(output)
		for {
			var pkg listedPackage
			if err := decoder.Decode(&pkg); err != nil {
				if err == io.EOF {
					break
				}
				return err
			}
			relativeDir, err := filepath.Rel(root, pkg.Dir)
			if err != nil || relativeDir == ".." || strings.HasPrefix(relativeDir, ".."+string(filepath.Separator)) {
				continue
			}
			for _, group := range [][]string{
				pkg.GoFiles, pkg.CgoFiles, pkg.CFiles, pkg.CXXFiles, pkg.MFiles,
				pkg.HFiles, pkg.FFiles, pkg.SFiles, pkg.SwigFiles, pkg.SwigCXXFiles,
				pkg.SysoFiles, pkg.EmbedFiles,
			} {
				for _, name := range group {
					files[filepath.Join(relativeDir, name)] = struct{}{}
				}
			}
		}
		if err := command.Wait(); err != nil {
			return fmt.Errorf("list linux/%s fixer build sources: %w", architecture, err)
		}
	}
	ordered := make([]string, 0, len(files))
	for name := range files {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	for _, relative := range ordered {
		mustCopy(root, destination, relative)
	}
	return nil
}

func mustCopy(root, destination, relative string) {
	mustCopyAs(root, destination, relative, relative)
}

func mustCopyAs(root, destination, sourceRelative, targetRelative string) {
	if err := copyPath(
		filepath.Join(root, sourceRelative), filepath.Join(destination, targetRelative),
	); err != nil {
		panic(fmt.Errorf("copy %s: %w", sourceRelative, err))
	}
}

func copyPath(source, destination string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			relative, err := filepath.Rel(source, path)
			if err != nil {
				return err
			}
			target := filepath.Join(destination, relative)
			if entry.IsDir() {
				return os.MkdirAll(target, 0o755)
			}
			return copyFile(path, target)
		})
	}
	return copyFile(source, destination)
}

func copyFile(source, destination string) error {
	switch filepath.Base(destination) {
	case "go.mod":
		destination = filepath.Join(filepath.Dir(destination), "wolf-embedded.go.mod")
	case "go.sum":
		destination = filepath.Join(filepath.Dir(destination), "wolf-embedded.go.sum")
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
