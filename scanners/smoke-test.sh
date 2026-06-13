#!/usr/bin/env bash
# shellcheck disable=SC2317  # check() and present() are dispatched via run() further down
# ============================================================
# wolf-scanners smoke test — variant-aware.
#
# Asserts every tool we BUNDLE in this variant is present and reports
# the pinned version. Tools that wolf serves via upstream-official
# images (semgrep, trivy, gitleaks, hadolint, etc.) are NOT checked
# here — they're separate `docker image inspect` checks at runtime via
# `wolf doctor`.
#
#   default  → small pure-Python core + per-language tools
#   jvm      → infer + pmd
#   rust     → clippy + cargo
#   codeql   → codeql
# ============================================================
set -euo pipefail

# shellcheck disable=SC1091
source /etc/wolf-scanners/versions.env

VARIANT="${WOLF_SCANNERS_VARIANT:-default}"

green() { printf '\033[0;32m%s\033[0m\n' "$1"; }
red()   { printf '\033[0;31m%s\033[0m\n' "$1" >&2; }

check() {
    local label="$1"; shift
    local expect="$1"; shift
    local out
    if ! out="$("$@" 2>&1)"; then
        red "FAIL $label: command failed: $*"
        return 1
    fi
    if [[ -n "$expect" && "$out" != *"$expect"* ]]; then
        red "FAIL $label: expected substring '$expect' not found"
        return 1
    fi
    green "  OK  $label"
    return 0
}

present() {
    local label="$1"; shift
    local bin="$1"; shift
    if ! command -v "$bin" >/dev/null 2>&1; then
        red "FAIL $label: $bin not on PATH"
        return 1
    fi
    green "  OK  $label"
    return 0
}

fails=0
run() { "$@" || fails=$((fails+1)); }

echo "wolf-scanners smoke test (variant: $VARIANT)"

if [[ "$VARIANT" == "default" ]]; then
    echo ""
    echo "[Bundled tools (default image)]"
    # Core small pure-Python.
    run check "detect-secrets $DETECT_SECRETS_VERSION" "$DETECT_SECRETS_VERSION" detect-secrets --version
    run check "sqlfluff $SQLFLUFF_VERSION"             "$SQLFLUFF_VERSION"        sqlfluff --version
    run check "yamllint $YAMLLINT_VERSION"             "$YAMLLINT_VERSION"        yamllint --version
    # Per-language tools.
    run check "bandit $BANDIT_VERSION"                 "$BANDIT_VERSION"          bandit --version
    run check "ruff $RUFF_VERSION"                     "$RUFF_VERSION"            ruff --version
    run check "mypy $MYPY_VERSION"                     "$MYPY_VERSION"            mypy --version
    run check "pip-audit $PIP_AUDIT_VERSION"           "$PIP_AUDIT_VERSION"       pip-audit --version
    run check "radon $RADON_VERSION"                   "$RADON_VERSION"           radon --version
    run check "vulture $VULTURE_VERSION"               "$VULTURE_VERSION"         vulture --version
    run present "gosec"                                                       gosec
    run present "staticcheck"                                                 staticcheck
    run check "govulncheck $GOVULNCHECK_VERSION"       "$GOVULNCHECK_VERSION"     govulncheck -version
    run present "gokart"                                                      gokart
    run check "eslint $ESLINT_VERSION"                 "$ESLINT_VERSION"          eslint --version
    run check "markdownlint $MARKDOWNLINT_VERSION"     "$MARKDOWNLINT_VERSION"    markdownlint --version
    run present "npm (for npm-audit)"                                         npm
    run check "brakeman $BRAKEMAN_VERSION"             "$BRAKEMAN_VERSION"        brakeman --version
    run check "rubocop $RUBOCOP_VERSION"               "$RUBOCOP_VERSION"         rubocop --version
    run check "phpstan $PHPSTAN_VERSION"               "$PHPSTAN_VERSION"         phpstan --version
    run present "swiftlint (best-effort)"                                     swiftlint
    run present "cppcheck"                                                    cppcheck
    run present "shellcheck"                                                  shellcheck
fi

if [[ "$VARIANT" == "jvm" ]]; then
    echo ""
    echo "[JVM tools]"
    # Infer doesn't ship a Linux/arm64 binary upstream — install
    # script skips it on aarch64. Match that here so the smoke test
    # doesn't spuriously red-flag a successful arm64 build.
    case "$(uname -m)" in
        aarch64|arm64) echo "  SKIP infer (no upstream linux/arm64 binary)" ;;
        *) run check "infer $INFER_VERSION" "$INFER_VERSION" infer --version ;;
    esac
    run check "pmd $PMD_VERSION" "$PMD_VERSION" pmd --version
    run check "detekt $DETEKT_VERSION" "$DETEKT_VERSION" detekt --version
fi

if [[ "$VARIANT" == "rust" ]]; then
    echo ""
    echo "[Rust tools]"
    run present "cargo-clippy" cargo-clippy
fi

if [[ "$VARIANT" == "codeql" ]]; then
    echo ""
    echo "[CodeQL]"
    run check "codeql $CODEQL_VERSION" "$CODEQL_VERSION" codeql --version
fi

echo ""
if [[ $fails -eq 0 ]]; then
    green "wolf-scanners smoke test ($VARIANT): all bundled tools present."
    exit 0
else
    red "wolf-scanners smoke test ($VARIANT): $fails tool(s) failed."
    if [[ "${WOLF_SMOKE_STRICT:-0}" == "1" ]]; then
        exit 1
    fi
    exit 0
fi
