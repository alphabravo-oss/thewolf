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

arch="$(uname -m)"
case "$arch" in
    x86_64|amd64)
        rustup_target=x86_64-unknown-linux-gnu
        rustup_sha256="$RUSTUP_LINUX_AMD64_SHA256"
        ;;
    aarch64|arm64)
        rustup_target=aarch64-unknown-linux-gnu
        rustup_sha256="$RUSTUP_LINUX_ARM64_SHA256"
        ;;
    *) echo "rustup: unsupported architecture $arch" >&2; exit 65 ;;
esac

rustup_tmp="$(mktemp -d)"
trap 'rm -rf "$rustup_tmp"' EXIT
curl --proto '=https' --tlsv1.2 --fail --location --silent --show-error \
    --proto-redir '=https' --retry 3 --retry-all-errors \
    "https://static.rust-lang.org/rustup/archive/${RUSTUP_VERSION}/${rustup_target}/rustup-init" \
    -o "${rustup_tmp}/rustup-init"
printf '%s  %s\n' "$rustup_sha256" "${rustup_tmp}/rustup-init" \
    | sha256sum --check --strict -
chmod 0755 "${rustup_tmp}/rustup-init"
"${rustup_tmp}/rustup-init" -y --no-modify-path \
    --default-toolchain "${RUST_TOOLCHAIN}" --profile minimal --component clippy
test "$("${CARGO_HOME}/bin/rustup" --version | awk '{print $2}')" = "$RUSTUP_VERSION"
rm -rf "$rustup_tmp"
trap - EXIT

# Symlink the toolchain binaries onto PATH.
for bin in cargo rustc rustup cargo-clippy clippy-driver; do
    ln -sf "${CARGO_HOME}/bin/${bin}" "/usr/local/bin/${bin}"
done

# rustup's proxy binaries resolve the selected toolchain through RUSTUP_HOME.
# Prove the exported runtime path on native builds. Cross-architecture QEMU
# can crash these binaries even when the artifact is valid; native matrix
# jobs and the post-build strict smoke remain mandatory.
if [[ "${WOLF_INSTALL_SMOKE_STRICT:-1}" == "1" ]]; then
    /usr/local/bin/rustc --version | grep -F "${RUST_TOOLCHAIN}"
    /usr/local/bin/cargo-clippy --version | grep -F clippy
fi

install_release_bin() {
    local url="$1" sha256="$2" bin_name="$3"
    local tmp extract
    tmp="$(mktemp -d)"
    extract="$(mktemp -d)"
    curl --proto '=https' --tlsv1.2 --fail --location --silent --show-error \
        --proto-redir '=https' --retry 3 --retry-all-errors \
        -o "${tmp}/archive" "$url"
    printf '%s  %s\n' "$sha256" "${tmp}/archive" | sha256sum --check --strict -
    tar -xzf "${tmp}/archive" -C "$extract"
    local found
    found="$(find "$extract" -type f -name "$bin_name" -print -quit)"
    if [[ -z "$found" ]]; then
        echo "rust.sh: $bin_name not found in $url" >&2
        exit 65
    fi
    install -m 0755 "$found" "/usr/local/bin/${bin_name}"
    rm -rf "$tmp" "$extract"
}

case "$arch" in
    x86_64|amd64)
        install_release_bin \
            "https://github.com/rustsec/rustsec/releases/download/cargo-audit%2Fv${CARGO_AUDIT_VERSION}/cargo-audit-x86_64-unknown-linux-musl-v${CARGO_AUDIT_VERSION}.tgz" \
            "$CARGO_AUDIT_LINUX_AMD64_SHA256" \
            cargo-audit
        install_release_bin \
            "https://github.com/EmbarkStudios/cargo-deny/releases/download/${CARGO_DENY_VERSION}/cargo-deny-${CARGO_DENY_VERSION}-x86_64-unknown-linux-musl.tar.gz" \
            "$CARGO_DENY_LINUX_AMD64_SHA256" \
            cargo-deny
        ;;
    aarch64|arm64)
        install_release_bin \
            "https://github.com/rustsec/rustsec/releases/download/cargo-audit%2Fv${CARGO_AUDIT_VERSION}/cargo-audit-aarch64-unknown-linux-gnu-v${CARGO_AUDIT_VERSION}.tgz" \
            "c6603814ddaa45e51263dafd31c0ac98808f688d26f7395804f9670b0fd599dd" \
            cargo-audit
        install_release_bin \
            "https://github.com/EmbarkStudios/cargo-deny/releases/download/${CARGO_DENY_VERSION}/cargo-deny-${CARGO_DENY_VERSION}-aarch64-unknown-linux-musl.tar.gz" \
            "995c82be0defc7a025cae49a2aa2644ce8245c9a3318fc4103907c6a285e8c7d" \
            cargo-deny
        ;;
esac

if [[ "${WOLF_INSTALL_SMOKE_STRICT:-1}" == "1" ]]; then
    /usr/local/bin/cargo-audit --version | grep -F "${CARGO_AUDIT_VERSION}"
    /usr/local/bin/cargo-deny --version | grep -F "${CARGO_DENY_VERSION}"
fi

# Drop docs & sources we never need at scan time.
rm -rf "${RUSTUP_HOME}/toolchains/${RUST_TOOLCHAIN}"*/share/doc 2>/dev/null || true

echo "Rust toolchain + clippy + cargo-audit + cargo-deny installed."
