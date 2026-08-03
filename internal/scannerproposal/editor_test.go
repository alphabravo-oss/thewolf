package scannerproposal

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/scannerproposalworker"
)

type editorRecordingRunner struct {
	commands []string
}

func (r *editorRecordingRunner) Run(
	_ context.Context,
	_ string,
	name string,
	args ...string,
) error {
	r.commands = append(r.commands, strings.Join(append([]string{name}, args...), " "))
	return nil
}

func TestSelectedUpdateEditorUsesDeterministicServerResolvedToolUpdate(t *testing.T) {
	t.Parallel()
	runner := &editorRecordingRunner{}
	editor := SelectedUpdateEditor{Runner: runner, GoPath: "go"}
	edit, err := editor.Edit(context.Background(), t.TempDir(), scannerproposalworker.Request{
		RiskSummary:   json.RawMessage(`{"highest_risk":"low"}`),
		RequiredGates: []string{"signature", "parser"}, SourceDateEpoch: 12345,
		Updates: []scannerproposalworker.SelectedUpdate{{
			ID: "update-semgrep", ComponentType: ChangeTool, ComponentName: "semgrep",
			CurrentValue: "1.0.0", AvailableValue: "1.0.1", RiskClass: "low",
			Evidence:      json.RawMessage(`{"source_url":"https://pypi.org/project/semgrep"}`),
			Compatibility: json.RawMessage(`{}`),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 1 || runner.commands[0] !=
		"go run ./cmd/scannertools bump --tool semgrep --version 1.0.1 --source-date-epoch 12345" {
		t.Fatalf("commands = %#v", runner.commands)
	}
	annotation := edit.ChangeAnnotations["tool:semgrep"]
	if annotation.Risk != "low" || annotation.EvidenceURL != "https://pypi.org/project/semgrep" ||
		len(edit.RequiredGates) != 2 || len(edit.Evidence) != 1 {
		t.Fatalf("edit = %#v", edit)
	}
}

func TestApplyBaseImageUpdatesMovesAllOwnedDockerfiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	oldDigest := "sha256:" + strings.Repeat("a", 64)
	newDigest := "sha256:" + strings.Repeat("b", 64)
	oldReference := "debian:trixie-slim@" + oldDigest
	newReference := "debian:trixie-slim@" + newDigest
	mustWriteTestFile(t, root, "scanners/toolchains.yaml", []byte(
		"base_images:\n  default: "+oldReference+"\n  jvm: "+oldReference+"\ntoolchains:\n  go:\n    version: \"1.2.3\"\n",
	), 0o644)
	for _, path := range []string{
		"scanners/Dockerfile", "scanners/Dockerfile.jvm", "fixer/Dockerfile.base",
	} {
		mustWriteTestFile(t, root, path, []byte("FROM "+oldReference+"\n"), 0o644)
	}
	err := applyBaseImageUpdates(root, []scannerproposalworker.SelectedUpdate{
		{ID: "base-default", ComponentName: "default", CurrentValue: oldDigest, AvailableDigest: newDigest},
		{ID: "base-jvm", ComponentName: "jvm", CurrentValue: oldDigest, AvailableDigest: newDigest},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"scanners/toolchains.yaml", "scanners/Dockerfile", "scanners/Dockerfile.jvm", "fixer/Dockerfile.base",
	} {
		value, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(value), oldReference) || !strings.Contains(string(value), newReference) {
			t.Fatalf("%s was not updated:\n%s", path, value)
		}
	}
}

func TestApplyGoToolchainUpdateBindsChecksumsInDefinitionAndInstaller(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustWriteTestFile(t, root, "scanners/toolchains.yaml", []byte(
		"base_images:\n  default: example@sha256:"+strings.Repeat("a", 64)+"\ntoolchains:\n  go:\n    version: \"1.2.3\"\n    linux_amd64_sha256: "+strings.Repeat("1", 64)+"\n    linux_arm64_sha256: "+strings.Repeat("2", 64)+"\n",
	), 0o644)
	mustWriteTestFile(t, root, "scanners/install/go-tools.sh", []byte(
		"GOTC_VERSION=1.2.3\nGOTC_LINUX_AMD64_SHA256="+strings.Repeat("1", 64)+"\nGOTC_LINUX_ARM64_SHA256="+strings.Repeat("2", 64)+"\n",
	), 0o755)
	amd64 := strings.Repeat("3", 64)
	arm64 := strings.Repeat("4", 64)
	err := applyGoToolchainUpdate(root, scannerproposalworker.SelectedUpdate{
		ID: "go", AvailableValue: "1.2.4",
		Evidence: json.RawMessage(`{"attributes":{"linux_amd64_sha256":"` + amd64 + `","linux_arm64_sha256":"` + arm64 + `"}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"scanners/toolchains.yaml", "scanners/install/go-tools.sh"} {
		value, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			t.Fatal(err)
		}
		for _, expected := range []string{"1.2.4", amd64, arm64} {
			if !strings.Contains(string(value), expected) {
				t.Fatalf("%s does not contain %s:\n%s", path, expected, value)
			}
		}
	}
}

func TestValidateSelectedChangesRejectsUnselectedLockMutation(t *testing.T) {
	t.Parallel()
	updates := []scannerproposalworker.SelectedUpdate{{
		ID: "selected", ComponentType: ChangeTool, ComponentName: "semgrep",
		AvailableValue: "1.0.1",
	}}
	err := validateSelectedChanges(updates, []Change{
		{Kind: ChangeTool, Name: "semgrep", To: "1.0.1"},
		{Kind: ChangeTool, Name: "bandit", To: "2.0.0"},
	})
	if err == nil || !strings.Contains(err.Error(), "unselected") {
		t.Fatalf("unselected lock mutation error = %v", err)
	}
}

func mustWriteTestFile(t *testing.T, root, relative string, value []byte, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, value, mode); err != nil {
		t.Fatal(err)
	}
}
