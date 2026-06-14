#!/usr/bin/env bash
# ============================================================
# Install GitHub CodeQL CLI + default query packs.
#
# CodeQL is the largest non-rust dependency in the image (~600 MB).
# License: free for analyzing open-source code; commercial use needs
# a GitHub Enterprise contract. Operators of wolf in commercial
# settings must confirm their CodeQL license; documented in
# scanners/LICENSES.md.
# ============================================================
set -euo pipefail

# shellcheck disable=SC1091
source /etc/wolf-scanners/versions.env

ARCH="$(uname -m)"
case "$ARCH" in
    x86_64)  CODEQL_ASSET="codeql-bundle-linux64.tar.gz" ;;
    aarch64) CODEQL_ASSET="codeql-bundle-linux64.tar.gz" ;; # codeql does not ship arm64 on linux as of v2.19
    *) echo "codeql: unsupported arch $ARCH" >&2; exit 1 ;;
esac

tmp="$(mktemp -d)"
curl -fsSL -o "${tmp}/codeql.tgz" \
    "https://github.com/github/codeql-action/releases/download/codeql-bundle-v${CODEQL_VERSION}/${CODEQL_ASSET}"
mkdir -p /opt/codeql
tar -xzf "${tmp}/codeql.tgz" -C /opt/codeql --strip-components=1
ln -sf /opt/codeql/codeql /usr/local/bin/codeql
rm -rf "${tmp}"

echo "CodeQL installed."
