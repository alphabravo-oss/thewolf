package scannerreleaseworkspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerpipeline"
)

func TestReadPlanEvidenceRequiresExactBoundSet(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	binding := testBinding()
	expected := []scannerpipeline.Step{
		{Key: "checkout", Kind: scannerpipeline.StepCheckout, Required: true, Timeout: time.Minute},
		{Key: "manifest-validate", Kind: scannerpipeline.StepValidation, Required: true, Timeout: time.Minute},
	}
	for _, step := range expected {
		if err := WriteEvidence(workspace, step, 1, binding, map[string]any{"digest": "sha256:" + strings.Repeat("a", 64)}); err != nil {
			t.Fatal(err)
		}
	}
	if evidence, err := ReadPlanEvidence(workspace, binding, expected); err != nil || len(evidence) != len(expected) {
		t.Fatalf("exact evidence = %#v, err=%v", evidence, err)
	}
	unexpected := scannerpipeline.Step{Key: "unexpected", Kind: scannerpipeline.StepEvidence, Required: true, Timeout: time.Minute}
	if err := WriteEvidence(workspace, unexpected, 1, binding, map[string]any{"status": "passed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPlanEvidence(workspace, binding, expected); err == nil || !strings.Contains(err.Error(), "unexpected") {
		t.Fatalf("unexpected evidence error = %v", err)
	}
	if _, err := ReadPlanEvidence(workspace, binding, append(expected, scannerpipeline.Step{Key: "missing"})); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing evidence error = %v", err)
	}
}

func TestReadAllEvidenceEnforcesCountAndEntryTypeBounds(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	directory := filepath.Join(workspace, ".wolf-release-evidence")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < maximumFiles+1; index++ {
		name := filepath.Join(directory, strings.Repeat("a", 60)+string(rune('A'+index%26))+strings.Repeat("b", index/26)+".json")
		if err := os.WriteFile(name, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ReadAllEvidence(workspace, testBinding()); err == nil || !strings.Contains(err.Error(), "file-count") {
		t.Fatalf("file-count error = %v", err)
	}

	workspace = t.TempDir()
	directory = filepath.Join(workspace, ".wolf-release-evidence")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(workspace, "outside.json")
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(directory, "evidence.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadAllEvidence(workspace, testBinding()); err == nil {
		t.Fatal("symlinked evidence entry was accepted")
	}
}

func TestWriteContextIsCredentialFreeAndImmutable(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	value := ExecutionContext{
		SourceURL: "https://git.example/wolf.git",
		Primary:   RegistryTarget{ID: "primary", Version: 1, Host: "registry.example", Namespace: "wolf", Repository: "wolf"},
		Mirror:    RegistryTarget{ID: "mirror", Version: 3, Host: "mirror.example", Namespace: "wolf", Repository: "wolf"},
	}
	if err := WriteContext(workspace, value); err != nil {
		t.Fatal(err)
	}
	if err := WriteContext(workspace, value); err != nil {
		t.Fatalf("idempotent context write: %v", err)
	}
	value.Mirror.Version++
	if err := WriteContext(workspace, value); err == nil {
		t.Fatal("context target mutation was accepted")
	}
	raw, err := os.ReadFile(filepath.Join(workspace, ".wolf-release-context.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(raw)), "password") || strings.Contains(strings.ToLower(string(raw)), "authorization") {
		t.Fatalf("context contains credential-shaped data: %s", raw)
	}
}

func testBinding() Binding {
	return NewBinding(
		"build-1", "candidate-1", 2, strings.Repeat("a", 40),
		"sha256:"+strings.Repeat("b", 64), "policy-1", 7,
	)
}
