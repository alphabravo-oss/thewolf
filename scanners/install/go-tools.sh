#!/usr/bin/env bash
# ============================================================
# Install Go-ecosystem scanners. Requires the go toolchain to
# be present (provided by the scanner image's locked OS package layer).
# ============================================================
set -euo pipefail

# shellcheck disable=SC1091
source /etc/wolf-scanners/versions.env

# Debian 12 ships an older golang-go; modern gosec/staticcheck/govulncheck
# need Go 1.22+. Install a current Go toolchain from go.dev.
GOTC_VERSION=1.26.5
GOTC_LINUX_AMD64_SHA256=5c2c3b16caefa1d968a94c1daca04a7ca301a496d9b086e17ad77bb81393f053
GOTC_LINUX_ARM64_SHA256=fe4789e92b1f33358680864bbe8704289e7bb5fc207d80623c308935bd696d49
ARCH="$(uname -m)"
case "$ARCH" in
    x86_64)
        GO_ARCH="amd64"
        GO_SHA256="$GOTC_LINUX_AMD64_SHA256"
        ;;
    aarch64)
        GO_ARCH="arm64"
        GO_SHA256="$GOTC_LINUX_ARM64_SHA256"
        ;;
    *) echo "go-tools: unsupported arch $ARCH" >&2; exit 1 ;;
esac

tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT
curl -fsSL -o "${tmp}/go.tgz" \
    "https://go.dev/dl/go${GOTC_VERSION}.linux-${GO_ARCH}.tar.gz"
printf '%s  %s\n' "$GO_SHA256" "${tmp}/go.tgz" | sha256sum -c -
rm -rf /usr/local/go-toolchain
mkdir -p /usr/local/go-toolchain
tar -xzf "${tmp}/go.tgz" -C /usr/local/go-toolchain --strip-components=1
export PATH="/usr/local/go-toolchain/bin:$PATH"
rm -rf "${tmp}"
trap - EXIT

export GOPATH=/opt/wolf-scanners/gopath
export GOBIN=/opt/wolf-scanners/gopath/bin
export GOCACHE=/tmp/gocache
mkdir -p "$GOPATH" "$GOBIN" "$GOCACHE"

install_go_tool() {
    local pkg="$1" binname="$2"
    if ! go build -mod=readonly -trimpath -o "${GOBIN}/${binname}" "$pkg"; then
        echo "WARNING: go build $pkg failed; skipping $binname"
        return 0
    fi
}

# Build every Go scanner from one committed module graph. This keeps the direct
# scanner versions and their security-sensitive transitive dependencies (most
# importantly gokart's go-git graph) deterministic and reviewable. `go install
# package@version` intentionally ignores the caller's go.mod and would silently
# restore upstream's stale transitive pins.
module_tmp="$(mktemp -d)"
trap 'rm -rf "${module_tmp}"' EXIT
cp -R /etc/wolf-scanners/go-tools/. "${module_tmp}/"
cd "${module_tmp}"
go mod download
install_go_tool "github.com/securego/gosec/v2/cmd/gosec" gosec
install_go_tool "honnef.co/go/tools/cmd/staticcheck" staticcheck
install_go_tool "golang.org/x/vuln/cmd/govulncheck" govulncheck
install_go_tool "github.com/praetorian-inc/gokart" gokart
cd /
rm -rf "${module_tmp}"
trap - EXIT

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

# Drop the module + build cache used only to compile the tools — not
# needed at runtime. gosec/govulncheck use `go list`/`go env`, which
# need GOROOT (src + bin), not GOPATH modules.
rm -rf "${GOPATH:?}/pkg" "${GOPATH:?}/bin"
# Strip toolchain dirs unused at runtime (docs, tests, api, examples).
rm -rf /usr/local/go-toolchain/{test,doc,api,misc}
# Go ships this private key only as an x509 test fixture. Runtime scanners do
# not need it and secret scanners correctly refuse to publish an image that
# contains private-key material, even when the key is public test data.
rm -f /usr/local/go-toolchain/src/crypto/x509/platform_root_key.pem

echo "Go scanners installed."
