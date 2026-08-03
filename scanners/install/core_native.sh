#!/usr/bin/env bash
# ============================================================
# Native tools bundled in the default image.
#   ShellCheck — cross-language shell script linter.
# (cppcheck is in lang_native.sh because it's language-specific.)
# ============================================================
set -euo pipefail

command -v shellcheck >/dev/null 2>&1 || {
    echo "shellcheck was not installed by the locked OS package layer" >&2
    exit 1
}

echo "Core native scanners installed."
