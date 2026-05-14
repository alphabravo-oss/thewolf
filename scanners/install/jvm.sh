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

# --- Infer ---
#
# Facebook only publishes infer Linux binaries for x86_64. The release
# pages have no linux-arm64 tarball at any version (checked v1.0–v1.3).
# Build-from-source would pull a multi-GB OCaml/clang toolchain and
# isn't worth bundling. On arm64 Linux containers we therefore install
# pmd only; the wolf infer plugin already guards on plugin.IsArm64Host
# and skips cleanly with an explanatory message on those hosts.
case "$ARCH" in
    x86_64)
        tmp="$(mktemp -d)"
        curl -fsSL -o "${tmp}/infer.tar.xz" \
            "https://github.com/facebook/infer/releases/download/v${INFER_VERSION}/infer-linux64-v${INFER_VERSION}.tar.xz"
        tar -xJf "${tmp}/infer.tar.xz" -C /opt
        mv "/opt/infer-linux64-v${INFER_VERSION}" /opt/infer
        ln -sf /opt/infer/bin/infer /usr/local/bin/infer
        rm -rf "${tmp}"
        ;;
    aarch64|arm64)
        echo "infer: no upstream Linux/arm64 binary — skipping install on this arch."
        ;;
    *)
        echo "infer: unsupported arch $ARCH — skipping install." >&2
        ;;
esac

# --- PMD ---
tmp="$(mktemp -d)"
curl -fsSL -o "${tmp}/pmd.zip" \
    "https://github.com/pmd/pmd/releases/download/pmd_releases%2F${PMD_VERSION}/pmd-dist-${PMD_VERSION}-bin.zip"
unzip -q -d /opt "${tmp}/pmd.zip"
mv "/opt/pmd-bin-${PMD_VERSION}" /opt/pmd
ln -sf /opt/pmd/bin/pmd /usr/local/bin/pmd
rm -rf "${tmp}"

echo "JVM scanners installed."
