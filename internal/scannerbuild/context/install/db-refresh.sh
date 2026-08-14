#!/usr/bin/env bash
# ============================================================
# Runtime vuln DB refresh. Run via:
#   docker run --rm -v wolf-scanners-db:/var/lib/wolf-db \
#     wolf-scanners:tag /usr/local/bin/db-refresh
#
# Controlled by WOLF_SCANNERS_DB_REFRESH in wolf-slim:
#   never — never run this script (default; use baked-in DBs).
#   once  — wolf-slim runs this once at startup.
#   each  — wolf-slim runs this before every scan (slow; not recommended).
# ============================================================
set -euo pipefail

echo "Refreshing vuln DBs..."

TRIVY_CACHE_DIR=/var/lib/wolf-db/trivy trivy image --download-db-only
GRYPE_DB_CACHE_DIR=/var/lib/wolf-db/grype grype db update

# osv-scanner uses the OSV.dev API on each scan; no local DB to refresh.
# govulncheck uses vuln.go.dev on each scan; no local DB.
# Bearer rule packs live in the separate bearer/bearer image (HOME on this
# volume at /var/lib/wolf-db/bearer) and hydrate on first language scan.

echo "Vuln DBs refreshed."
