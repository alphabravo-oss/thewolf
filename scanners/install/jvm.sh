#!/usr/bin/env bash
# ============================================================
# Install JVM-ecosystem scanners (Infer + PMD).
# This script targets the wolf-scanners-jvm image, which has
# openjdk-17-jdk in the base.
# ============================================================
set -euo pipefail

# shellcheck disable=SC1091
source /etc/wolf-scanners/versions.env

ARCH="$(uname -m)"
case "$ARCH" in
    x86_64)  INFER_ARCH="linux64" ;;
    aarch64) INFER_ARCH="linux-arm64" ;;
    *) echo "infer: unsupported arch $ARCH" >&2; exit 1 ;;
esac

# --- Infer ---
tmp="$(mktemp -d)"
curl -fsSL -o "${tmp}/infer.tar.xz" \
    "https://github.com/facebook/infer/releases/download/v${INFER_VERSION}/infer-${INFER_ARCH}-v${INFER_VERSION}.tar.xz"
tar -xJf "${tmp}/infer.tar.xz" -C /opt
mv "/opt/infer-${INFER_ARCH}-v${INFER_VERSION}" /opt/infer
ln -sf /opt/infer/bin/infer /usr/local/bin/infer
rm -rf "${tmp}"

# --- PMD ---
tmp="$(mktemp -d)"
curl -fsSL -o "${tmp}/pmd.zip" \
    "https://github.com/pmd/pmd/releases/download/pmd_releases%2F${PMD_VERSION}/pmd-dist-${PMD_VERSION}-bin.zip"
unzip -q -d /opt "${tmp}/pmd.zip"
mv "/opt/pmd-bin-${PMD_VERSION}" /opt/pmd
ln -sf /opt/pmd/bin/pmd /usr/local/bin/pmd
rm -rf "${tmp}"

echo "JVM scanners installed."
