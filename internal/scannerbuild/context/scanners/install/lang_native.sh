#!/usr/bin/env bash
# ============================================================
# Native language scanners — cppcheck (C/C++).
# ============================================================
set -euo pipefail

command -v cppcheck >/dev/null 2>&1 || {
    echo "cppcheck was not installed by the locked OS package layer" >&2
    exit 1
}

echo "C/C++ scanners installed."
