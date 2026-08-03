package main

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	scannerlock "github.com/alphabravocompany/thewolf/internal/scannertools/lock"
)

const proposalSchema = "wolf.scanners.proposal/v1"

var scannerProposalInputs = []string{
	".dockerignore", "go.mod", "go.sum", "scanners", "fixer", "cmd", "internal", "plugins",
	"docs/superpowers/changelog/scanner-bumps",
}

type proposalChange struct {
	Tool    string `json:"tool"`
	Version string `json:"version"`
}

type proposalChanges []proposalChange

func (c *proposalChanges) String() string {
	values := make([]string, len(*c))
	for index, change := range *c {
		values[index] = change.Tool + "=" + change.Version
	}
	return strings.Join(values, ",")
}

func (c *proposalChanges) Set(value string) error {
	tool, version, ok := strings.Cut(value, "=")
	tool, version = strings.TrimSpace(tool), strings.TrimSpace(version)
	if !ok || tool == "" || version == "" {
		return errorsNewUpdateFormat(value)
	}
	*c = append(*c, proposalChange{Tool: tool, Version: version})
	return nil
}

func errorsNewUpdateFormat(value string) error {
	return fmt.Errorf("invalid --update %q; expected TOOL=VERSION", value)
}

type proposalMetadata struct {
	SchemaVersion    string           `json:"schema_version"`
	CandidateKey     string           `json:"candidate_key"`
	DefinitionCommit string           `json:"definition_commit"`
	BaseLockDigest   string           `json:"base_lock_digest"`
	LockDigest       string           `json:"lock_digest"`
	PatchDigest      string           `json:"patch_digest"`
	GeneratedAt      time.Time        `json:"generated_at"`
	Changes          []proposalChange `json:"changes"`
	ApplyCommand     string           `json:"apply_command"`
}

func runPropose(ctx context.Context, root string, args []string) error {
	fs := flag.NewFlagSet("propose", flag.ContinueOnError)
	var changes proposalChanges
	fs.Var(&changes, "update", "scanner update in TOOL=VERSION form (repeatable)")
	output := fs.String("output", "", "proposal bundle output path (.tar)")
	jsonOutput := fs.Bool("json", false, "print machine-readable proposal metadata")
	allowDirty := fs.Bool("allow-dirty-definition", false, "allow uncommitted scanner definition inputs")
	sourceDateEpoch := fs.Int64("source-date-epoch", sourceDateEpochDefault(), "deterministic proposal timestamp")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("propose does not accept positional arguments")
	}
	if len(changes) == 0 {
		return fmt.Errorf("propose requires at least one --update TOOL=VERSION")
	}
	if strings.TrimSpace(*output) == "" {
		return fmt.Errorf("propose requires --output")
	}
	if *sourceDateEpoch < 0 {
		return fmt.Errorf("--source-date-epoch must not be negative")
	}
	generatedAt := time.Unix(*sourceDateEpoch, 0).UTC()
	normalized, err := normalizeProposalChanges(changes)
	if err != nil {
		return err
	}
	definitionCommit, err := gitOutput(ctx, root, nil, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("resolve definition commit: %w", err)
	}
	if !*allowDirty {
		statusArgs := append(
			[]string{"status", "--porcelain=v1", "--"},
			scannerProposalInputs...,
		)
		status, err := gitOutput(ctx, root, nil, statusArgs...)
		if err != nil {
			return fmt.Errorf("inspect definition worktree: %w", err)
		}
		if status != "" {
			return fmt.Errorf("scanner definition has uncommitted changes; commit them or use --allow-dirty-definition")
		}
	}
	baseLock, err := scannerlock.LoadFile(filepath.Join(root, scannerlock.DefaultLockPath))
	if err != nil {
		return fmt.Errorf("load base scanner lock: %w", err)
	}

	workspace, err := os.MkdirTemp("", "wolf-scanner-proposal-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workspace)
	for _, relative := range scannerProposalInputs {
		if err := copyProposalInput(root, workspace, relative); err != nil {
			return err
		}
	}
	if _, err := gitOutput(ctx, workspace, nil, "init", "--quiet"); err != nil {
		return err
	}
	if _, err := gitOutput(ctx, workspace, nil, "add", "--all"); err != nil {
		return err
	}
	commitEnvironment := []string{
		"GIT_AUTHOR_NAME=Wolf Scanner Release",
		"GIT_AUTHOR_EMAIL=scanner-release@invalid",
		"GIT_COMMITTER_NAME=Wolf Scanner Release",
		"GIT_COMMITTER_EMAIL=scanner-release@invalid",
		"GIT_AUTHOR_DATE=" + generatedAt.Format(time.RFC3339),
		"GIT_COMMITTER_DATE=" + generatedAt.Format(time.RFC3339),
	}
	if _, err := gitOutput(ctx, workspace, commitEnvironment, "commit", "--quiet", "-m", "scanner proposal baseline"); err != nil {
		return err
	}
	for _, change := range normalized {
		if err := bumpToolAt(workspace, change.Tool, change.Version, generatedAt, io.Discard); err != nil {
			return fmt.Errorf("apply proposed update %s=%s: %w", change.Tool, change.Version, err)
		}
	}
	if _, err := gitOutput(ctx, workspace, nil, "add", "--intent-to-add", "--all"); err != nil {
		return err
	}
	patch, err := gitOutputBytes(ctx, workspace, nil,
		"diff", "--binary", "--no-ext-diff", "--full-index", "HEAD", "--",
	)
	if err != nil {
		return fmt.Errorf("render proposal patch: %w", err)
	}
	if len(bytes.TrimSpace(patch)) == 0 {
		return fmt.Errorf("proposed updates produced no definition diff")
	}
	if len(patch) > 32<<20 {
		return fmt.Errorf("proposal patch exceeds 32 MiB")
	}
	proposedLock, err := scannerlock.LoadFile(filepath.Join(workspace, scannerlock.DefaultLockPath))
	if err != nil {
		return fmt.Errorf("load proposed scanner lock: %w", err)
	}
	patchDigest := digestProposalBytes(patch)
	candidateKey := proposalCandidateKey(
		strings.TrimSpace(definitionCommit), proposedLock.LockDigest, normalized,
	)
	metadata := proposalMetadata{
		SchemaVersion: proposalSchema, CandidateKey: candidateKey,
		DefinitionCommit: strings.TrimSpace(definitionCommit),
		BaseLockDigest:   baseLock.LockDigest, LockDigest: proposedLock.LockDigest,
		PatchDigest: patchDigest, GeneratedAt: generatedAt, Changes: normalized,
		ApplyCommand: "tar -xf proposal.tar && git apply --index changes.patch",
	}
	metadataBytes, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	metadataBytes = append(metadataBytes, '\n')
	bundle, err := proposalTar(metadataBytes, patch, generatedAt)
	if err != nil {
		return err
	}
	outputPath := *output
	if !filepath.IsAbs(outputPath) {
		outputPath = filepath.Join(root, outputPath)
	}
	if err := writeFileAtomic(outputPath, bundle, 0o640); err != nil {
		return err
	}
	if *jsonOutput {
		result, err := json.Marshal(struct {
			proposalMetadata
			Path         string `json:"path"`
			BundleDigest string `json:"bundle_digest"`
		}{
			proposalMetadata: metadata, Path: outputPath,
			BundleDigest: digestProposalBytes(bundle),
		})
		if err != nil {
			return err
		}
		fmt.Println(string(result))
		return nil
	}
	fmt.Printf("wrote scanner proposal %s (%s)\n", outputPath, candidateKey)
	return nil
}

func normalizeProposalChanges(changes []proposalChange) ([]proposalChange, error) {
	byTool := make(map[string]string, len(changes))
	for _, change := range changes {
		if existing, duplicate := byTool[change.Tool]; duplicate && existing != change.Version {
			return nil, fmt.Errorf("scanner tool %q has conflicting proposed versions", change.Tool)
		}
		byTool[change.Tool] = change.Version
	}
	out := make([]proposalChange, 0, len(byTool))
	for tool, version := range byTool {
		out = append(out, proposalChange{Tool: tool, Version: version})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tool < out[j].Tool })
	return out, nil
}

func copyProposalInput(root, workspace, relative string) error {
	source := filepath.Join(root, filepath.FromSlash(relative))
	info, err := os.Lstat(source)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	target := filepath.Join(workspace, filepath.FromSlash(relative))
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("proposal input %s must not be a symlink", relative)
	}
	if !info.IsDir() {
		data, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	}
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relativePath, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(workspace, relativePath)
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("proposal input %s must not be a symlink", relativePath)
		}
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o750)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destination, data, info.Mode().Perm())
	})
}

func gitOutput(
	ctx context.Context,
	directory string,
	extraEnvironment []string,
	args ...string,
) (string, error) {
	value, err := gitOutputBytes(ctx, directory, extraEnvironment, args...)
	return strings.TrimSpace(string(value)), err
}

func gitOutputBytes(
	ctx context.Context,
	directory string,
	extraEnvironment []string,
	args ...string,
) ([]byte, error) {
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = directory
	command.Env = append(os.Environ(), append([]string{"GIT_CONFIG_NOSYSTEM=1"}, extraEnvironment...)...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	value, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return value, nil
}

func proposalTar(metadata, patch []byte, timestamp time.Time) ([]byte, error) {
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	for _, file := range []struct {
		name string
		data []byte
		mode int64
	}{
		{name: "proposal.json", data: metadata, mode: 0o640},
		{name: "changes.patch", data: patch, mode: 0o640},
	} {
		header := &tar.Header{
			Name: file.name, Mode: file.mode, Size: int64(len(file.data)),
			ModTime: timestamp, AccessTime: timestamp, ChangeTime: timestamp,
			Typeflag: tar.TypeReg, Format: tar.FormatPAX,
		}
		if err := writer.WriteHeader(header); err != nil {
			return nil, err
		}
		if _, err := writer.Write(file.data); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func proposalCandidateKey(
	definitionCommit, lockDigest string,
	changes []proposalChange,
) string {
	value, _ := json.Marshal(struct {
		DefinitionCommit string           `json:"definition_commit"`
		LockDigest       string           `json:"lock_digest"`
		Changes          []proposalChange `json:"changes"`
	}{definitionCommit, lockDigest, changes})
	return digestProposalBytes(value)
}

func digestProposalBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func sourceDateEpochDefault() int64 {
	if value := strings.TrimSpace(os.Getenv("SOURCE_DATE_EPOCH")); value != "" {
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil && parsed >= 0 {
			return parsed
		}
	}
	return time.Now().UTC().Truncate(24 * time.Hour).Unix()
}
