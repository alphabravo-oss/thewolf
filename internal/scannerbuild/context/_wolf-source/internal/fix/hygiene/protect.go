package hygiene

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
)

// ProtectedRel is a high-level "do not break the product" path.
// Helm packaging, vendored chart tarballs, and published API contracts
// are restored if a formatter or agent rewrites them.
func ProtectedRel(rel string) bool {
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	rel = strings.TrimPrefix(rel, "./")
	low := strings.ToLower(rel)
	base := filepath.Base(low)
	switch base {
	case "chart.yaml", "chart.yml", "chart.lock":
		return true
	case "openapi.yaml", "openapi.yml", "openapi.json",
		"swagger.yaml", "swagger.yml", "swagger.json":
		return true
	}
	if strings.HasSuffix(low, ".tgz") || strings.HasSuffix(low, ".tgz.prov") {
		if strings.Contains(low, "/charts/") || strings.HasPrefix(low, "charts/") {
			return true
		}
	}
	if strings.Contains(low, "/openapi/") || strings.Contains(low, "/swagger/") {
		return true
	}
	return false
}

// RestoreProtected checks out any dirty protected files so a turn cannot
// delete a vendored Helm chart or rewrite an API contract.
func RestoreProtected(ctx context.Context, repoPath string) []string {
	if repoPath == "" {
		return nil
	}
	out, err := exec.CommandContext(ctx, "git", "-C", repoPath, "status", "--porcelain").Output() // #nosec G204
	if err != nil || len(out) == 0 {
		return nil
	}
	var restored []string
	for _, rel := range porcelainPaths(out) {
		if !ProtectedRel(rel) {
			continue
		}
		_ = exec.CommandContext(ctx, "git", "-C", repoPath, "checkout", "--", rel).Run() // #nosec G204
		// Deleted files need restore from HEAD; checkout -- handles both.
		restored = append(restored, rel)
	}
	return restored
}

func porcelainPaths(raw []byte) []string {
	var out []string
	for _, line := range strings.Split(string(raw), "\n") {
		if len(line) < 4 {
			continue
		}
		path := strings.TrimSpace(line[3:])
		if i := strings.Index(path, " -> "); i >= 0 {
			path = path[i+4:]
		}
		path = strings.Trim(path, `"`)
		path = filepath.ToSlash(path)
		if path != "" {
			out = append(out, path)
		}
	}
	return out
}
