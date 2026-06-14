#!/usr/bin/env bash
# ============================================================
# Build-time vuln DB bake-in.
# Runs at image build time so the first scan after pull doesn't
# pay the vuln-DB download tax.
# ============================================================
set -euo pipefail

mkdir -p /var/lib/wolf-db/trivy /var/lib/wolf-db/grype /var/lib/wolf-db/semgrep

# trivy: download vuln DB into the volume location.
TRIVY_CACHE_DIR=/var/lib/wolf-db/trivy trivy image --download-db-only 2>&1 \
    | grep -v "DEBUG" || true

# grype: same.
GRYPE_DB_CACHE_DIR=/var/lib/wolf-db/grype grype db update 2>&1 \
    | grep -v "DEBUG" || true

# semgrep: pre-download the default ruleset.
SEMGREP_RULES_CACHE_DIR=/var/lib/wolf-db/semgrep \
    semgrep --config "p/default" --version >/dev/null 2>&1 || true

# Permissions so any uid can read the cache when mounted as a volume.
chmod -R a+rX /var/lib/wolf-db

echo "Vuln DBs baked."
