#!/usr/bin/env bash
# ============================================================
# Install scanners distributed as GitHub release tarballs.
#
# As of wolf 2.0, the well-supported multi-arch tools (trivy, grype,
# syft, osv-scanner, gitleaks, trufflehog, hadolint, dockle, checkov,
# tflint, kubescape, kube-linter, nuclei, vale, spectral,
# scorecard) are NOT installed here — wolf's container shim uses the
# maintainers' upstream-official images for those (see
# internal/plugin/container/buckets.go DefaultUpstreamTools).
#
# What remains is the short tail of tools that don't have an
# upstream image and that we can install from a release tarball.
# Currently: none. This script is preserved as a hook for future
# tarball-based tools.
# ============================================================
set -uo pipefail

# shellcheck disable=SC1091
source /etc/wolf-scanners/versions.env

echo "downloads.sh: no tarball-based tools to install for this variant."
