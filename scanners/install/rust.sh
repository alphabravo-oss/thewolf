#!/usr/bin/env bash
# ============================================================
# Install the Rust toolchain + clippy via rustup.
# This is the largest single component of wolf-scanners (~1.2 GB).
# ============================================================
set -euo pipefail

# shellcheck disable=SC1091
source /etc/wolf-scanners/versions.env

export RUSTUP_HOME=/opt/wolf-scanners/rustup
export CARGO_HOME=/opt/wolf-scanners/cargo
mkdir -p "$RUSTUP_HOME" "$CARGO_HOME"

curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs \
    | sh -s -- -y --default-toolchain "${RUST_TOOLCHAIN}" --profile minimal \
        --component clippy

# Symlink the toolchain binaries onto PATH.
for bin in cargo rustc rustup cargo-clippy clippy-driver; do
    ln -sf "${CARGO_HOME}/bin/${bin}" "/usr/local/bin/${bin}"
done

# Drop docs & sources we never need at scan time.
rm -rf "${RUSTUP_HOME}/toolchains/${RUST_TOOLCHAIN}"*/share/doc 2>/dev/null || true

echo "Rust toolchain + clippy installed."
