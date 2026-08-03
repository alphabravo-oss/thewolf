package main

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannertools/manifest"
)

func TestNormalizeProposalChangesSortsAndDeduplicates(t *testing.T) {
	t.Parallel()

	changes, err := normalizeProposalChanges([]proposalChange{
		{Tool: "semgrep", Version: "1.2.3"},
		{Tool: "bandit", Version: "2.0.0"},
		{Tool: "semgrep", Version: "1.2.3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 || changes[0].Tool != "bandit" || changes[1].Tool != "semgrep" {
		t.Fatalf("changes = %#v", changes)
	}
	if _, err := normalizeProposalChanges([]proposalChange{
		{Tool: "semgrep", Version: "1.2.3"},
		{Tool: "semgrep", Version: "2.0.0"},
	}); err == nil {
		t.Fatal("conflicting proposal versions accepted")
	}
}

func TestProposalTarIsDeterministicAndContainsOnlySafeFiles(t *testing.T) {
	t.Parallel()

	timestamp := time.Unix(1_785_427_200, 0).UTC()
	first, err := proposalTar([]byte(`{"schema_version":"test"}`), []byte("patch"), timestamp)
	if err != nil {
		t.Fatal(err)
	}
	second, err := proposalTar([]byte(`{"schema_version":"test"}`), []byte("patch"), timestamp)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("proposal tar is not byte deterministic")
	}
	reader := tar.NewReader(bytes.NewReader(first))
	var names []string
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, header.Name)
		if header.Typeflag != tar.TypeReg || header.ModTime.UTC() != timestamp {
			t.Fatalf("unsafe/non-deterministic header = %#v", header)
		}
	}
	if len(names) != 2 || names[0] != "proposal.json" || names[1] != "changes.patch" {
		t.Fatalf("bundle entries = %#v", names)
	}
}

func TestRunProposeCreatesReproducibleApplicableBundle(t *testing.T) {
	t.Parallel()

	sourceRoot, err := manifest.FindRepoRoot("")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, relative := range scannerProposalInputs {
		if err := copyProposalInput(sourceRoot, root, relative); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := gitOutput(context.Background(), root, nil, "init", "--quiet"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitOutput(context.Background(), root, nil, "add", "--all"); err != nil {
		t.Fatal(err)
	}
	environment := []string{
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@invalid",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@invalid",
	}
	if _, err := gitOutput(context.Background(), root, environment, "commit", "--quiet", "-m", "baseline"); err != nil {
		t.Fatal(err)
	}
	loaded, err := manifest.LoadFile(filepath.Join(root, "scanners", "tools.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	semgrep := loaded.Tools["semgrep"]
	if semgrep.PinnedVersion == "" {
		t.Fatal("semgrep fixture has no pinned version")
	}
	args := []string{
		"--update", "semgrep=99.99.99",
		"--source-date-epoch", "1785427200",
	}
	firstPath := filepath.Join(root, "first-proposal.tar")
	if err := runPropose(context.Background(), root, append(args, "--output", firstPath)); err != nil {
		t.Fatal(err)
	}
	secondPath := filepath.Join(root, "second-proposal.tar")
	if err := runPropose(context.Background(), root, append(args, "--output", secondPath)); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("identical proposal inputs produced different bundles")
	}

	entries := readProposalEntries(t, first)
	var metadata proposalMetadata
	if err := json.Unmarshal(entries["proposal.json"], &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.SchemaVersion != proposalSchema ||
		metadata.PatchDigest != digestProposalBytes(entries["changes.patch"]) ||
		metadata.BaseLockDigest == metadata.LockDigest {
		t.Fatalf("proposal metadata = %#v", metadata)
	}
	patchPath := filepath.Join(root, "changes.patch")
	if err := os.WriteFile(patchPath, entries["changes.patch"], 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := gitOutput(context.Background(), root, nil, "apply", "--check", patchPath); err != nil {
		t.Fatalf("proposal patch is not applicable: %v", err)
	}
}

func readProposalEntries(t *testing.T, bundle []byte) map[string][]byte {
	t.Helper()
	out := make(map[string][]byte)
	reader := tar.NewReader(bytes.NewReader(bundle))
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		value, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		out[header.Name] = value
	}
}
