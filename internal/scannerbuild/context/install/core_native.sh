#!/usr/bin/env bash
# ============================================================
# Native tools (apt) bundled in the default image.
#   shellcheck — cross-language shell script linter.
# (cppcheck is in lang_native.sh because it's language-specific.)
# ============================================================
set -euo pipefail

export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y --no-install-recommends \
    shellcheck
apt-get clean
rm -rf /var/lib/apt/lists/*

echo "Core native scanners installed."
