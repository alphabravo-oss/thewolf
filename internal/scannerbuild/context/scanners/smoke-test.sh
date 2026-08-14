#!/usr/bin/env bash
# shellcheck disable=SC2016,SC2317,SC2329  # dpkg format is literal; helpers are dispatched via run()
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
#   rust     → clippy + cargo + cargo-audit + cargo-deny
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

check_go_module() {
	local label="$1"; shift
	local expect="$1"; shift
	local bin="$1"; shift
	local path out
	path="$(command -v "$bin")" || {
		red "FAIL $label: $bin not on PATH"
		return 1
	}
	if ! out="$("$GO_BIN" version -m "$path" 2>&1)"; then
		red "FAIL $label: could not inspect Go module build identity"
		return 1
	fi
	if [[ "$out" != *"$expect"* ]]; then
		red "FAIL $label: expected module version '$expect' not found"
		return 1
	fi
	green "  OK  $label"
}

check_locked_deb() {
	local label="$1"; shift
	local package="$1"; shift
	local architecture pin_file expected installed
	architecture="$(dpkg --print-architecture)"
	pin_file="/etc/wolf-scanners/os-packages/pins/${VARIANT}-${architecture}.txt"
	expected="$(awk -F= -v package="$package" -v architecture="$architecture" \
		'$1 == package || $1 == package ":" architecture { print $2; exit }' "$pin_file")"
	installed="$(dpkg-query -W -f='${Version}' "$package" 2>/dev/null || true)"
	if [[ -z "$expected" || "$installed" != "$expected" ]]; then
		red "FAIL $label: installed package '$installed' does not match locked '$expected'"
		return 1
	fi
	green "  OK  $label $installed"
}

check_locked_nodejs() {
	local architecture artifact_file filename expected installed
	architecture="$(dpkg --print-architecture)"
	artifact_file="/etc/wolf-scanners/os-packages/artifacts/${VARIANT}-${architecture}.txt"
	filename="$(awk -F '\t' '$1 == "nodejs" { print $4; exit }' "$artifact_file")"
	expected="${filename#nodejs_}"
	expected="${expected%_"${architecture}".deb}"
	installed="$(dpkg-query -W -f='${Version}' nodejs 2>/dev/null || true)"
	if [[ -z "$filename" || "$installed" != "$expected" ]]; then
		red "FAIL nodejs/npm-audit: installed package '$installed' does not match locked '$expected'"
		return 1
	fi
	green "  OK  nodejs/npm-audit package $installed"
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
	run check "core pip vendored msgpack 1.2.1" "1.2.1" /opt/wolf-scanners/py-core/bin/python -c 'import pip._vendor.msgpack as m; print(m.__version__)'
	run check "language pip vendored msgpack 1.2.1" "1.2.1" /opt/wolf-scanners/py-lang/bin/python -c 'import pip._vendor.msgpack as m; print(m.__version__)'
    run check "radon $RADON_VERSION"                   "$RADON_VERSION"           radon --version
    run check "vulture $VULTURE_VERSION"               "$VULTURE_VERSION"         vulture --version
    GO_BIN="$(command -v go || echo /usr/local/go-toolchain/bin/go)"
    run check_go_module "gosec $GOSEC_VERSION module identity" "$GOSEC_VERSION" gosec
    run check "staticcheck $STATICCHECK_VERSION" "$STATICCHECK_VERSION" staticcheck -version
    run check "govulncheck $GOVULNCHECK_VERSION"       "$GOVULNCHECK_VERSION"     govulncheck -version
    run check_go_module "gokart $GOKART_VERSION module identity" "$GOKART_VERSION" gokart
    # The Go toolchain is retained (not the GOPATH module cache) so that
    # gosec/govulncheck can resolve std-lib modules at runtime via the
    # stripped toolchain. Prove GOROOT (src + bin) still resolves the
    # standard library after the slimming in go-tools.sh.
    run check "go env GOROOT" "/usr/local/go-toolchain" "$GO_BIN" env GOROOT
    run check "go list std (toolchain resolves modules)" "fmt" "$GO_BIN" list std
    run check "eslint $ESLINT_VERSION"                 "$ESLINT_VERSION"          eslint --version
    run check "markdownlint $MARKDOWNLINT_VERSION"     "$MARKDOWNLINT_VERSION"    markdownlint --version
	run check_locked_nodejs
	run check "npm $NPM_VERSION" "$NPM_VERSION" npm --version
	run check "npm bundled brace-expansion 5.0.9" "5.0.9" node -p 'require("/usr/lib/node_modules/npm/node_modules/brace-expansion/package.json").version'
	run check "npm bundled picomatch 4.0.5" "4.0.5" node -p 'require("/usr/lib/node_modules/npm/node_modules/picomatch/package.json").version'
	run check "npm bundled ip-address 10.3.1" "10.3.1" node -p 'require("/usr/lib/node_modules/npm/node_modules/ip-address/package.json").version'
    run check "brakeman $BRAKEMAN_VERSION"             "$BRAKEMAN_VERSION"        brakeman --version
    run check "rubocop $RUBOCOP_VERSION"               "$RUBOCOP_VERSION"         rubocop --version
    run check "phpstan $PHPSTAN_VERSION"               "$PHPSTAN_VERSION"         phpstan --version
	run check_locked_deb "cppcheck" cppcheck
	run check "cppcheck invocation" "Cppcheck" cppcheck --version
	run check_locked_deb "shellcheck" shellcheck
	run check "shellcheck invocation" "ShellCheck" shellcheck --version
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
    run check "rust toolchain $RUST_TOOLCHAIN (owns cargo-clippy)" "$RUST_TOOLCHAIN" rustc --version
    run check "cargo-clippy from rust $RUST_TOOLCHAIN" "clippy" cargo-clippy --version
    run check "cargo-audit $CARGO_AUDIT_VERSION" "$CARGO_AUDIT_VERSION" cargo-audit --version
    run check "cargo-deny $CARGO_DENY_VERSION" "$CARGO_DENY_VERSION" cargo-deny --version
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
