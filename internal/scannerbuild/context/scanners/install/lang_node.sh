#!/usr/bin/env bash
# ============================================================
# Install Node language scanners — eslint. npm-audit is bundled
# with npm itself (no separate install).
# ============================================================
set -euo pipefail

# shellcheck disable=SC1091
source /etc/wolf-scanners/versions.env

# NodeSource includes npm, but the npm release cadence and security fixes are
# independent from Node. Pin and upgrade npm before installing scanner CLIs so
# the final image does not retain vulnerable bundled tar/glob/sigstore modules.
npm install -g --omit=dev --omit=optional "npm@${NPM_VERSION}"
npm install -g --omit=dev --omit=optional \
    "eslint@${ESLINT_VERSION}" \
    "markdownlint-cli@${MARKDOWNLINT_VERSION}"

echo "Node language scanners installed."
