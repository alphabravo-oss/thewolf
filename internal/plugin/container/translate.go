package container

import "strings"

// ScanMountPoint is the path inside every scanner container where the repo
// is bind-mounted. All plugins should pass "/scan" as the repo target in
// their tool arguments.
const ScanMountPoint = "/scan"

// OutMountPoint is the path inside every scanner container where transient
// outputs (codeql databases, etc.) may be written. Backed by a per-scan
// tmpfs by default.
const OutMountPoint = "/out"

// NormalizePath strips the container's /scan/ prefix from a path so that
// findings stored in the wolf database use stable, repo-relative paths.
//
// Examples:
//
//	NormalizePath("/scan/foo/bar.py")  → "foo/bar.py"
//	NormalizePath("/scan")             → ""
//	NormalizePath("./relative")        → "./relative"   (unchanged)
//	NormalizePath("/abs/path")         → "/abs/path"    (unchanged)
//
// Why repo-relative paths? Two reasons:
//
//  1. **Portability.** Findings are stored with fingerprints derived from
//     ToolName + RuleID + FilePath. If FilePath is an absolute host path,
//     fingerprints differ between two hosts scanning the same repo. With
//     repo-relative paths, fingerprints are stable.
//
//  2. **Display.** The wolf UI shows findings keyed by repo + path. A
//     repo-relative path renders directly; an absolute container path
//     ("/scan/...") would need post-processing in the UI.
//
// Paths that don't start with /scan are returned unchanged. This is a
// defensive choice: some scanners (e.g. trivy --sbom) emit findings about
// packages with no file path, or with a synthetic identifier. Passing those
// through unchanged is correct.
func NormalizePath(p string) string {
	if p == ScanMountPoint {
		return ""
	}
	const prefix = ScanMountPoint + "/"
	if strings.HasPrefix(p, prefix) {
		return strings.TrimPrefix(p, prefix)
	}
	return p
}

// ContainerSubPath converts a host-side absolute path that lives under
// repoDir into the corresponding /scan-rooted container path.
//
//	ContainerSubPath("/home/me/myrepo", "/home/me/myrepo/cmd/foo") → "/scan/cmd/foo"
//	ContainerSubPath("/home/me/myrepo", "/home/me/myrepo")         → "/scan"
//
// Returns "/scan" if subPath does not live under repoDir (defensive default
// — the caller has likely passed an unexpected path).
//
// This helper is used by plugins like gosec/infer/codeql that locate a
// build-root subdirectory on the host and need to set --workdir for the
// container invocation.
func ContainerSubPath(repoDir, subPath string) string {
	if subPath == "" || repoDir == "" {
		return ScanMountPoint
	}
	if subPath == repoDir {
		return ScanMountPoint
	}
	if len(subPath) > len(repoDir) && subPath[:len(repoDir)] == repoDir && subPath[len(repoDir)] == '/' {
		return ScanMountPoint + subPath[len(repoDir):]
	}
	return ScanMountPoint
}

// NormalizePaths applies NormalizePath to every entry in the slice in place.
// Convenience for plugins that produce findings with paths from the scanner's
// JSON output.
func NormalizePaths(paths []string) {
	for i := range paths {
		paths[i] = NormalizePath(paths[i])
	}
}
