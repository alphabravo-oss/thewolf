#!/usr/bin/env bash
# Pin lint/format/fix CLIs into the fixer image. Invoked from Dockerfile.base
# and Dockerfile.engines. Does not install scanners (trivy, semgrep, …).
set -euo pipefail

versions_file="${1:-/usr/share/wolf/fixer/versions.env}"
# shellcheck disable=SC1090
source "$versions_file"

arch="$(dpkg --print-architecture 2>/dev/null || uname -m)"
case "$arch" in
  amd64|x86_64)
    rust_target=x86_64-unknown-linux-gnu
    yamlfmt_arch=x86_64
    shfmt_arch=amd64
    hadolint_arch=x86_64
    shellcheck_arch=x86_64
    golangci_arch=amd64
    taplo_arch=x86_64
    ruff_sha="$RUFF_LINUX_AMD64_SHA256"
    yamlfmt_sha="$YAMLFMT_LINUX_AMD64_SHA256"
    shfmt_sha="$SHFMT_LINUX_AMD64_SHA256"
    uv_sha="$UV_LINUX_AMD64_SHA256"
    hadolint_sha="$HADOLINT_LINUX_AMD64_SHA256"
    shellcheck_sha="$SHELLCHECK_LINUX_AMD64_SHA256"
    golangci_sha="$GOLANGCI_LINT_LINUX_AMD64_SHA256"
    taplo_sha="$TAPLO_LINUX_AMD64_SHA256"
    ;;
  arm64|aarch64)
    rust_target=aarch64-unknown-linux-gnu
    yamlfmt_arch=arm64
    shfmt_arch=arm64
    hadolint_arch=arm64
    shellcheck_arch=aarch64
    golangci_arch=arm64
    taplo_arch=aarch64
    ruff_sha="$RUFF_LINUX_ARM64_SHA256"
    yamlfmt_sha="$YAMLFMT_LINUX_ARM64_SHA256"
    shfmt_sha="$SHFMT_LINUX_ARM64_SHA256"
    uv_sha="$UV_LINUX_ARM64_SHA256"
    hadolint_sha="$HADOLINT_LINUX_ARM64_SHA256"
    shellcheck_sha="$SHELLCHECK_LINUX_ARM64_SHA256"
    golangci_sha="$GOLANGCI_LINT_LINUX_ARM64_SHA256"
    taplo_sha="$TAPLO_LINUX_ARM64_SHA256"
    ;;
  *)
    echo "unsupported architecture: $arch" >&2
    exit 65
    ;;
esac

DEST=/opt/wolf/fix-tools
BIN="$DEST/bin"
NODE="$DEST/node"
mkdir -p "$BIN" "$NODE"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

download() {
  local url="$1" sha="$2" out="$3"
  curl --proto '=https' --tlsv1.2 --fail --location --silent --show-error \
    --proto-redir '=https' --retry 3 --retry-all-errors \
    "$url" -o "$out"
  printf '%s  %s\n' "$sha" "$out" | sha256sum --check --strict -
}

install_bin() {
  local src="$1" name="$2"
  install -m 0755 "$src" "$BIN/$name"
  ln -sfn "$BIN/$name" "/usr/local/bin/$name"
}

# --- standalone formatters / linters ---------------------------------------

download \
  "https://github.com/astral-sh/ruff/releases/download/${RUFF_VERSION}/ruff-${rust_target}.tar.gz" \
  "$ruff_sha" "$TMP/ruff.tgz"
tar -C "$TMP" -xzf "$TMP/ruff.tgz"
install_bin "$(find "$TMP" -type f -name ruff | head -n1)" ruff

download \
  "https://github.com/google/yamlfmt/releases/download/v${YAMLFMT_VERSION}/yamlfmt_${YAMLFMT_VERSION}_Linux_${yamlfmt_arch}.tar.gz" \
  "$yamlfmt_sha" "$TMP/yamlfmt.tgz"
tar -C "$TMP" -xzf "$TMP/yamlfmt.tgz"
install_bin "$TMP/yamlfmt" yamlfmt

download \
  "https://github.com/mvdan/sh/releases/download/v${SHFMT_VERSION}/shfmt_v${SHFMT_VERSION}_linux_${shfmt_arch}" \
  "$shfmt_sha" "$TMP/shfmt"
install_bin "$TMP/shfmt" shfmt

download \
  "https://github.com/astral-sh/uv/releases/download/${UV_VERSION}/uv-${rust_target}.tar.gz" \
  "$uv_sha" "$TMP/uv.tgz"
tar -C "$TMP" -xzf "$TMP/uv.tgz"
install_bin "$(find "$TMP" -type f -name uv | head -n1)" uv
if uvx_src="$(find "$TMP" -type f -name uvx | head -n1)"; then
  install_bin "$uvx_src" uvx
fi

download \
  "https://github.com/hadolint/hadolint/releases/download/v${HADOLINT_VERSION}/hadolint-linux-${hadolint_arch}" \
  "$hadolint_sha" "$TMP/hadolint"
install_bin "$TMP/hadolint" hadolint

download \
  "https://github.com/koalaman/shellcheck/releases/download/v${SHELLCHECK_VERSION}/shellcheck-v${SHELLCHECK_VERSION}.linux.${shellcheck_arch}.tar.xz" \
  "$shellcheck_sha" "$TMP/shellcheck.txz"
tar -C "$TMP" -xJf "$TMP/shellcheck.txz"
install_bin "$(find "$TMP" -type f -name shellcheck | head -n1)" shellcheck

download \
  "https://github.com/golangci/golangci-lint/releases/download/v${GOLANGCI_LINT_VERSION}/golangci-lint-${GOLANGCI_LINT_VERSION}-linux-${golangci_arch}.tar.gz" \
  "$golangci_sha" "$TMP/golangci.tgz"
tar -C "$TMP" -xzf "$TMP/golangci.tgz"
install_bin "$(find "$TMP" -type f -name golangci-lint | head -n1)" golangci-lint

download \
  "https://github.com/tamasfe/taplo/releases/download/${TAPLO_VERSION}/taplo-linux-${taplo_arch}.gz" \
  "$taplo_sha" "$TMP/taplo.gz"
gzip -dc "$TMP/taplo.gz" >"$TMP/taplo"
install_bin "$TMP/taplo" taplo

# --- npm formatters (isolated prefixes, never mutate global npm) ------------

install_npm() {
  local pkg="$1" ver="$2" integ="$3" bin="$4"
  local actual
  actual="$(npm --cache /tmp/npm-cache view "${pkg}@${ver}" dist.integrity)"
  test "$actual" = "$integ"
  npm --cache /tmp/npm-cache install --prefix "$NODE/$pkg" --omit=dev "${pkg}@${ver}"
  ln -sfn "$NODE/$pkg/node_modules/.bin/$bin" "$BIN/$bin"
  ln -sfn "$BIN/$bin" "/usr/local/bin/$bin"
}

install_npm prettier "$PRETTIER_VERSION" "$PRETTIER_INTEGRITY" prettier
install_npm markdownlint-cli "$MARKDOWNLINT_CLI_VERSION" "$MARKDOWNLINT_CLI_INTEGRITY" markdownlint
install_npm eslint "$ESLINT_VERSION" "$ESLINT_INTEGRITY" eslint

rm -rf /tmp/npm-cache

# --- smoke ------------------------------------------------------------------
test "$(prettier --version)" = "$PRETTIER_VERSION"
markdownlint --version >/dev/null
eslint --version >/dev/null
test "$(ruff --version | awk '{print $2}')" = "$RUFF_VERSION"
yamlfmt --version >/dev/null
shfmt --version >/dev/null
uv --version >/dev/null
hadolint --version >/dev/null
shellcheck --version >/dev/null
golangci-lint version >/dev/null
taplo --version >/dev/null
