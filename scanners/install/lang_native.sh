#!/usr/bin/env bash
# ============================================================
# Native language scanners — cppcheck (C/C++).
# ============================================================
set -euo pipefail

export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y --no-install-recommends \
    cppcheck
apt-get clean
rm -rf /var/lib/apt/lists/*

echo "C/C++ scanners installed."
