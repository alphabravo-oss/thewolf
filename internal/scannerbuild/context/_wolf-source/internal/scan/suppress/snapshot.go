package suppress

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"

	gitcmd "github.com/alphabravocompany/thewolf/internal/fix/git"
	"github.com/alphabravocompany/thewolf/internal/models"
)

// Snapshot retains the effective rules and Git decisions for a scan's observed
// paths. Git's matcher remains authoritative, including nested rules/negation;
// historical reads need neither the checkout nor the process environment.
// Durable Wolf suppressions are intentionally separate and remain editable.
type Snapshot struct {
	SourceKey string          `json:"source_key,omitempty"`
	Version   int             `json:"version"`
	Rules     RuleSet         `json:"rules"`
	Paths     map[string]bool `json:"git_ignored_paths"`
}

// ScanSourceKey binds stored policy to the scan's source identity, not mutable
// progress fields. JSON string encoding keeps arbitrary paths unambiguous.
func ScanSourceKey(scan *models.Scan) string {
	remote := ""
	if scan.RemoteNodeID != nil {
		remote = *scan.RemoteNodeID
	}
	data, _ := json.Marshal([]string{scan.ID, scan.RepoID, scan.Branch, string(scan.SourceType), scan.SourcePath, scan.PreparedWorkspace, scan.CommitSHA, scan.TreeDigest, scan.SourceFingerprint, remote})
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func Capture(ctx context.Context, findings []models.Finding, repoPath string) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	var wolf RuleSet
	if repoPath != "" {
		var err error
		wolf, err = ParseWolfIgnoreFile(filepath.Join(repoPath, ".wolfignore"))
		if err != nil {
			return Snapshot{}, fmt.Errorf("load suppression rules: %w", err)
		}
	}
	snapshot := Snapshot{Version: 1, Rules: Combine(DefaultRules(), wolf), Paths: make(map[string]bool)}
	var paths []string
	for _, f := range findings {
		if _, seen := snapshot.Paths[f.FilePath]; !seen {
			snapshot.Paths[f.FilePath] = false
			paths = append(paths, f.FilePath)
		}
	}
	ignored, err := gitcmd.IgnoredPaths(ctx, repoPath, paths)
	if err != nil {
		return Snapshot{}, err
	}
	for path := range snapshot.Paths {
		snapshot.Paths[path] = ignored[path]
	}
	return snapshot, nil
}

func (s Snapshot) Apply(findings []models.Finding) ([]models.Finding, int, error) {
	if s.Version != 1 || s.Paths == nil {
		return findings, 0, fmt.Errorf("unsupported or incomplete scan ignore snapshot")
	}
	// Validate before changing any row. An unknown path cannot silently acquire
	// today's Git policy or be mistaken for an explicitly unignored path.
	for _, f := range findings {
		if _, known := s.Paths[f.FilePath]; !known {
			return findings, 0, fmt.Errorf("finding path is absent from scan ignore snapshot")
		}
	}
	projected, count := Apply(slices.Clone(findings), s.Rules)
	for i := range projected {
		if !projected[i].Suppressed && s.Paths[projected[i].FilePath] {
			projected[i].Suppressed = true
			projected[i].SuppressedReason = gitignoreReason
			count++
		}
	}
	copy(findings, projected)
	return findings, count, nil
}

// ApplyScan uses immutable policy where available. Uncaptured terminal scans keep
// their persisted decisions: reconstructing historical rules from today's
// checkout would invent evidence. Running, uncaptured scans still use live rules.
func ApplyScan(ctx context.Context, findings []models.Finding, scan *models.Scan, repoPath string) ([]models.Finding, int, error) {
	if err := ctx.Err(); err != nil {
		return findings, 0, err
	}
	if scan.IgnorePolicyJSON != "" {
		var snapshot Snapshot
		if err := json.Unmarshal([]byte(scan.IgnorePolicyJSON), &snapshot); err != nil {
			return findings, 0, fmt.Errorf("decode scan ignore snapshot: %w", err)
		}
		if snapshot.SourceKey != "" && snapshot.SourceKey != ScanSourceKey(scan) {
			return findings, 0, fmt.Errorf("scan source identity differs from captured ignore policy")
		}
		return snapshot.Apply(findings)
	}
	if scan.Status == models.ScanStatusCompleted || scan.Status == models.ScanStatusFailed || scan.Status == models.ScanStatusCancelled {
		return findings, 0, nil
	}
	return ApplyRepo(ctx, findings, repoPath)
}
