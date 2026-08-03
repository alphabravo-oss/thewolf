#!/usr/bin/env bash
# ============================================================
# Install SwiftLint from the precompiled portable release.
# SwiftLint's portable Linux release is amd64-only. Supported architectures
# fail closed on download, checksum, archive, or layout errors.
# ============================================================
set -euo pipefail

# shellcheck disable=SC1091
source /etc/wolf-scanners/versions.env

arch="$(uname -m)"
case "$arch" in
    x86_64)  asset="portable_swiftlint.zip" ;;
    aarch64|arm64)
        echo "swiftlint: upstream portable release is linux/amd64-only — skipping"
        exit 0
        ;;
    *) echo "swiftlint: unsupported arch $arch — skipping" >&2; exit 0 ;;
esac

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
curl --proto '=https' --tlsv1.2 --fail --location --silent --show-error \
    --proto-redir '=https' --retry 3 --retry-all-errors \
    "https://github.com/realm/SwiftLint/releases/download/${SWIFTLINT_VERSION}/${asset}" \
    -o "${tmp}/swiftlint.zip"
printf '%s  %s\n' "$SWIFTLINT_PORTABLE_SHA256" "${tmp}/swiftlint.zip" \
    | sha256sum --check --strict -
unzip -q -d "${tmp}/swiftlint" "${tmp}/swiftlint.zip"
test -f "${tmp}/swiftlint/swiftlint"
install -m 0755 "${tmp}/swiftlint/swiftlint" /usr/local/bin/swiftlint
rm -rf "${tmp}"
trap - EXIT
echo "SwiftLint installed."
