package scannerreleaseworker

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

const (
	buildWorkspaceSchema         = "wolf.scanner-release-build-workspace/v1"
	buildWorkspaceBinding        = ".wolf-release-workspace.json"
	maximumWorkspaceBindingBytes = 64 << 10
)

type workspaceBinding struct {
	SchemaVersion    string `json:"schema_version"`
	BuildRunID       string `json:"build_run_id"`
	CandidateID      string `json:"candidate_id"`
	BuildAttempt     int    `json:"build_attempt"`
	DefinitionCommit string `json:"definition_commit"`
	LockDigest       string `json:"lock_digest"`
	PolicyID         string `json:"policy_id"`
	PolicyRevision   int64  `json:"policy_revision"`
}

func expectedWorkspaceBinding(
	build *scannerrelease.BuildRun,
	candidate *scannerrelease.Candidate,
	policy *scannerrelease.Policy,
) workspaceBinding {
	return workspaceBinding{
		SchemaVersion: buildWorkspaceSchema,
		BuildRunID:    build.ID, CandidateID: candidate.ID,
		BuildAttempt:     build.Attempt,
		DefinitionCommit: scannerrelease.EffectiveDefinitionCommit(candidate),
		LockDigest:       candidate.LockDigest,
		PolicyID:         policy.ID, PolicyRevision: policy.Revision,
	}
}

func deterministicWorkspaceName(buildID string) string {
	sum := sha256.Sum256([]byte(buildID))
	return "wolf-scanner-release-" + hex.EncodeToString(sum[:])
}

func (w *Worker) prepareBuildWorkspace(
	build *scannerrelease.BuildRun,
	candidate *scannerrelease.Candidate,
	policy *scannerrelease.Policy,
) (string, error) {
	root, err := prepareWorkspaceRoot(w.config.WorkspaceRoot)
	if err != nil {
		return "", err
	}
	workspace := filepath.Join(root, deterministicWorkspaceName(build.ID))
	if err := ensureWorkspaceUnderRoot(root, workspace); err != nil {
		return "", err
	}
	if err := os.Mkdir(workspace, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", err
	}
	info, err := os.Lstat(workspace)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("scanner release build workspace must be a real directory")
	}
	expected := expectedWorkspaceBinding(build, candidate, policy)
	path := filepath.Join(workspace, buildWorkspaceBinding)
	existing, found, err := readWorkspaceBinding(path)
	if err != nil {
		return "", err
	}
	if found {
		if existing != expected {
			return "", errors.New("scanner release build workspace immutable binding mismatch")
		}
		return workspace, nil
	}
	entries, err := os.ReadDir(workspace)
	if err != nil {
		return "", err
	}
	if len(entries) != 0 {
		return "", errors.New("unbound scanner release build workspace is not empty")
	}
	value, err := json.Marshal(expected)
	if err != nil {
		return "", err
	}
	if err := writeWorkspaceBinding(path, value); err != nil {
		return "", err
	}
	return workspace, nil
}

func prepareWorkspaceRoot(raw string) (string, error) {
	root := filepath.Clean(raw)
	if !filepath.IsAbs(root) {
		return "", errors.New("scanner release workspace root must be absolute")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("scanner release workspace root must be a real directory")
	}
	return root, nil
}

func ensureWorkspaceUnderRoot(root, workspace string) error {
	relative, err := filepath.Rel(root, workspace)
	if err != nil || relative == "." || relative == ".." ||
		filepath.IsAbs(relative) || filepath.Clean(relative) != relative ||
		filepath.Dir(relative) != "." {
		return errors.New("scanner release build workspace escapes its configured root")
	}
	return nil
}

func readWorkspaceBinding(path string) (workspaceBinding, bool, error) {
	pathInfo, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return workspaceBinding{}, false, nil
	}
	if err != nil {
		return workspaceBinding{}, false, err
	}
	if !pathInfo.Mode().IsRegular() || pathInfo.Mode()&os.ModeSymlink != 0 ||
		pathInfo.Size() > maximumWorkspaceBindingBytes {
		return workspaceBinding{}, false, errors.New("scanner release workspace binding is not a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return workspaceBinding{}, false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return workspaceBinding{}, false, err
	}
	if !info.Mode().IsRegular() || info.Size() > maximumWorkspaceBindingBytes ||
		!os.SameFile(pathInfo, info) {
		return workspaceBinding{}, false, errors.New("scanner release workspace binding is not a bounded regular file")
	}
	value, err := io.ReadAll(io.LimitReader(file, maximumWorkspaceBindingBytes+1))
	if err != nil {
		return workspaceBinding{}, false, err
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	var binding workspaceBinding
	if err := decoder.Decode(&binding); err != nil {
		return workspaceBinding{}, false, fmt.Errorf("decode scanner release workspace binding: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return workspaceBinding{}, false, errors.New("scanner release workspace binding has trailing JSON")
	}
	return binding, true, nil
}

func writeWorkspaceBinding(path string, value []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".workspace-binding-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(value); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func (w *Worker) removeBuildWorkspace(buildID string) error {
	root, err := prepareWorkspaceRoot(w.config.WorkspaceRoot)
	if err != nil {
		return err
	}
	workspace := filepath.Join(root, deterministicWorkspaceName(buildID))
	if err := ensureWorkspaceUnderRoot(root, workspace); err != nil {
		return err
	}
	info, err := os.Lstat(workspace)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("refusing to clean a non-directory scanner release workspace")
	}
	return w.config.RemoveAll(workspace)
}
