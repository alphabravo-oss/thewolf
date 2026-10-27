package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

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
		defer func() { _ = os.RemoveAll(temporary) }()
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
	defer func() { _ = input.Close() }()
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
