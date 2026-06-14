#!/usr/bin/env bash
# ============================================================
# Install SwiftLint from the precompiled portable release.
# SwiftLint's Linux/ARM64 support is partial — best-effort install.
# ============================================================
set -uo pipefail

# shellcheck disable=SC1091
source /etc/wolf-scanners/versions.env

arch="$(uname -m)"
case "$arch" in
    x86_64)  asset="portable_swiftlint.zip" ;;
    aarch64) asset="portable_swiftlint.zip" ;;
    *) echo "swiftlint: unsupported arch $arch — skipping" >&2; exit 0 ;;
esac

tmp="$(mktemp -d)"
if ! curl -fsSL -o "${tmp}/swiftlint.zip" \
    "https://github.com/realm/SwiftLint/releases/download/${SWIFTLINT_VERSION}/${asset}"; then
    echo "WARNING: swiftlint download failed (likely no ${arch} release); skipping"
    rm -rf "${tmp}"
    exit 0
fi
if ! unzip -q -d "${tmp}/swiftlint" "${tmp}/swiftlint.zip" 2>/dev/null; then
    echo "WARNING: swiftlint unzip failed; skipping"
    rm -rf "${tmp}"
    exit 0
fi
if [[ -f "${tmp}/swiftlint/swiftlint" ]]; then
    install -m 0755 "${tmp}/swiftlint/swiftlint" /usr/local/bin/swiftlint
    echo "SwiftLint installed."
else
    echo "WARNING: swiftlint binary not found in package; skipping"
fi
rm -rf "${tmp}"
