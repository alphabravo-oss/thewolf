package scannerbuild

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMaterialize(t *testing.T) {
	dir := t.TempDir()
	if err := Materialize(dir); err != nil {
		t.Fatalf("Materialize: %v", err)
	}

	dockerfile := filepath.Join(dir, "Dockerfile")
	if _, err := os.Stat(dockerfile); err != nil {
		t.Fatalf("expected Dockerfile materialized: %v", err)
	}

	goTools := filepath.Join(dir, "install", "go-tools.sh")
	info, err := os.Stat(goTools)
	if err != nil {
		t.Fatalf("expected install/go-tools.sh materialized: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("install/go-tools.sh is not executable, mode=%v", info.Mode().Perm())
	}
}
