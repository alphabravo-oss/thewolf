#!/usr/bin/env bash
# ============================================================
# Install Node language scanners — eslint. npm-audit is bundled
# with npm itself (no separate install).
# ============================================================
set -euo pipefail

# shellcheck disable=SC1091
source /etc/wolf-scanners/versions.env

npm install -g --omit=dev --omit=optional \
    "eslint@${ESLINT_VERSION}" \
    "markdownlint-cli@${MARKDOWNLINT_VERSION}"

npm cache clean --force >/dev/null 2>&1 || true

echo "Node language scanners installed."
