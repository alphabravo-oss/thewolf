#!/usr/bin/env bash
# ============================================================
# Install Go-ecosystem scanners. Requires the go toolchain to
# be present (provided by the golang base or apt install).
# ============================================================
set -uo pipefail

# shellcheck disable=SC1091
source /etc/wolf-scanners/versions.env

# Debian 12 ships an older golang-go; modern gosec/staticcheck/govulncheck
# need Go 1.22+. Install a current Go toolchain from go.dev.
GOTC_VERSION=1.23.4
ARCH="$(uname -m)"
case "$ARCH" in
    x86_64)  GO_ARCH="amd64" ;;
    aarch64) GO_ARCH="arm64" ;;
    *) echo "go-tools: unsupported arch $ARCH" >&2; exit 0 ;;
esac

tmp="$(mktemp -d)"
if curl -fsSL -o "${tmp}/go.tgz" \
    "https://go.dev/dl/go${GOTC_VERSION}.linux-${GO_ARCH}.tar.gz"; then
    rm -rf /usr/local/go-toolchain
    mkdir -p /usr/local/go-toolchain
    tar -xzf "${tmp}/go.tgz" -C /usr/local/go-toolchain --strip-components=1
    export PATH="/usr/local/go-toolchain/bin:$PATH"
else
    echo "WARNING: failed to fetch Go ${GOTC_VERSION}; falling back to debian's golang-go"
fi
rm -rf "${tmp}"

export GOPATH=/opt/wolf-scanners/gopath
export GOBIN=/opt/wolf-scanners/gopath/bin
export GOCACHE=/tmp/gocache
mkdir -p "$GOPATH" "$GOBIN" "$GOCACHE"

install_go_tool() {
    local pkg="$1" binname="$2"
    if ! go install "$pkg"; then
        echo "WARNING: go install $pkg failed; skipping $binname"
        return 0
    fi
}

install_go_tool "github.com/securego/gosec/v2/cmd/gosec@v${GOSEC_VERSION}"          gosec
install_go_tool "honnef.co/go/tools/cmd/staticcheck@v${STATICCHECK_VERSION}"        staticcheck
install_go_tool "golang.org/x/vuln/cmd/govulncheck@v${GOVULNCHECK_VERSION}"         govulncheck
install_go_tool "github.com/praetorian-inc/gokart@v${GOKART_VERSION}"               gokart

# Move binaries to /usr/local/bin so we can drop the go toolchain
# (we don't need it at runtime — staticcheck/gosec/govulncheck are
# self-contained binaries).
for bin in gosec staticcheck govulncheck gokart; do
    if [[ -f "${GOBIN}/${bin}" ]]; then
        install -m 0755 "${GOBIN}/${bin}" "/usr/local/bin/${bin}"
    else
        echo "WARNING: ${bin} not built; skipping symlink"
    fi
done

# Cleanup: drop go build cache, but keep the Go toolchain because
# govulncheck/gosec invoke `go list` / `go env` at runtime on scanned
# repos that need module resolution. (See plugins/go/*.go for usage.)
rm -rf "$GOCACHE"

echo "Go scanners installed."
