#!/usr/bin/env bash
# ============================================================
# Install Ruby-ecosystem scanners (brakeman, rubocop).
# Requires the ruby + gem runtime in the base image.
# ============================================================
set -euo pipefail

# shellcheck disable=SC1091
source /etc/wolf-scanners/versions.env

gem install --no-document \
    "brakeman:${BRAKEMAN_VERSION}" \
    "rubocop:${RUBOCOP_VERSION}"

# Wrappers land in /usr/local/bin/<gem> already via gem's default.
echo "Ruby scanners installed."
