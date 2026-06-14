#!/usr/bin/env bash
# ============================================================
# Install JVM-ecosystem scanners (Infer + PMD + detekt).
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
        # Facebook changed the asset naming at v1.2.0:
        #   v1.1.x → infer-linux64-v<ver>.tar.xz
        #   v1.2.0+ → infer-linux-x86_64-v<ver>.tar.xz
        # Try the new format first, fall back to the legacy one so we
        # can still pin older versions if needed.
        tmp="$(mktemp -d)"
        url_new="https://github.com/facebook/infer/releases/download/v${INFER_VERSION}/infer-linux-x86_64-v${INFER_VERSION}.tar.xz"
        url_old="https://github.com/facebook/infer/releases/download/v${INFER_VERSION}/infer-linux64-v${INFER_VERSION}.tar.xz"
        if curl -fsSL -o "${tmp}/infer.tar.xz" "$url_new"; then
            tar -xJf "${tmp}/infer.tar.xz" -C /opt
            mv "/opt/infer-linux-x86_64-v${INFER_VERSION}" /opt/infer
        else
            curl -fsSL -o "${tmp}/infer.tar.xz" "$url_old"
            tar -xJf "${tmp}/infer.tar.xz" -C /opt
            mv "/opt/infer-linux64-v${INFER_VERSION}" /opt/infer
        fi
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

# --- detekt ---
tmp="$(mktemp -d)"
curl -fsSL -o "${tmp}/detekt.zip" \
    "https://github.com/detekt/detekt/releases/download/v${DETEKT_VERSION}/detekt-cli-${DETEKT_VERSION}.zip"
unzip -q -d /opt "${tmp}/detekt.zip"
mv "/opt/detekt-cli-${DETEKT_VERSION}" /opt/detekt
ln -sf /opt/detekt/bin/detekt-cli /usr/local/bin/detekt-cli
ln -sf /opt/detekt/bin/detekt-cli /usr/local/bin/detekt
rm -rf "${tmp}"

echo "JVM scanners installed."
