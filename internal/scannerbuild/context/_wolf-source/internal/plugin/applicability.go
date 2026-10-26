package plugin

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
	"gopkg.in/yaml.v3"
)

// NotApplicableError distinguishes absent inputs from unavailable scanners.
type NotApplicableError struct{ Reason string }

func (e *NotApplicableError) Error() string { return "not applicable: " + e.Reason }

func IsNotApplicable(err error) bool {
	var skip *NotApplicableError
	return errors.As(err, &skip)
}

// SkippedError retains existing plugin skips (including missing configuration)
// without claiming either successful coverage or absent repository inputs.
type SkippedError struct{ Reason string }

func (e *SkippedError) Error() string { return "skipped: " + e.Reason }

func IsSkipped(err error) bool {
	var skip *SkippedError
	return IsNotApplicable(err) || errors.As(err, &skip)
}

// CheckApplicability is shared by preflight planning and execution. Plugins
// without an input detector remain eligible; inspection errors never mean skip.
func CheckApplicability(p models.Plugin, repoPath string, targets ...string) error {
	target := ""
	if len(targets) > 0 {
		target = targets[0]
	}
	if repoPath == "" {
		return nil
	}
	if checker, ok := p.(interface{ Applicability(string, string) error }); ok {
		return checker.Applicability(repoPath, target)
	}
	return nil
}

func KubernetesApplicability(repoPath string) error {
	present, err := HasKubernetesInputs(repoPath)
	if err != nil {
		return err
	}
	if !present {
		return &NotApplicableError{Reason: "no Kubernetes manifests or Helm charts found"}
	}
	return nil
}

// HasKubernetesInputs recognizes manifests at any depth, including multi-document
// YAML and Helm charts. Unreadable inputs are errors, not evidence of absence.
func HasKubernetesInputs(dir string) (bool, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return false, err
	}
	defer func() { _ = root.Close() }() // read-only walk; close errors carry no data
	found := false
	err = fs.WalkDir(root.FS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", ".venv", "dist", "build":
				return fs.SkipDir
			}
			return nil
		}
		if d.Name() == "Chart.yaml" {
			found = true
			return fs.SkipAll
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".yaml", ".yml", ".json":
		default:
			return nil
		}
		file, err := root.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		// Large or malformed candidate files still go to the scanner. They
		// must not become false evidence that Kubernetes inputs are absent.
		data, err := io.ReadAll(io.LimitReader(file, 1024*1024+1))
		if err != nil {
			return err
		}
		if len(data) > 1024*1024 {
			found = true
			return fs.SkipAll
		}
		decoder := yaml.NewDecoder(strings.NewReader(string(data)))
		for {
			var resource struct {
				APIVersion string `yaml:"apiVersion"`
				Kind       string `yaml:"kind"`
			}
			err := decoder.Decode(&resource)
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err != nil || (resource.APIVersion != "" && resource.Kind != "") {
				found = true
				return fs.SkipAll
			}
		}
	})
	return found, err
}
