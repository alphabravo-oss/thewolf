#!/usr/bin/env bash
set -euo pipefail

versions_file="${1:-/usr/share/wolf/fixer/versions.env}"
# shellcheck disable=SC1090
source "$versions_file"

override_tmp="$(mktemp -d)"
trap 'rm -rf "${override_tmp}"' EXIT
global_root="$(npm root --global)"

# Replace whole directories so npm upgrades cannot retain stale private
# dependencies. Verify downloaded bytes, including TypeScript 7's native CLI.
install_package() {
    local package="$1" version="$2" integrity="$3" archive actual destination
    archive="$(cd "$override_tmp" && npm pack --silent "${package}@${version}")"
    actual="sha512-$(openssl dgst -sha512 -binary "${override_tmp}/${archive}" | openssl base64 -A)"
    test "$actual" = "$integrity"
    destination="${global_root}/${package}"
    rm -rf "$destination"
    mkdir -p "$destination"
    tar -xzf "${override_tmp}/${archive}" --strip-components=1 -C "$destination"
}

install_package npm "$NPM_VERSION" "$NPM_INTEGRITY"
install_package typescript "$TYPESCRIPT_VERSION" "$TYPESCRIPT_INTEGRITY"
case "$(dpkg --print-architecture)" in
    amd64) native_package=@typescript/typescript-linux-x64; native_integrity="$TYPESCRIPT_LINUX_AMD64_INTEGRITY" ;;
    arm64) native_package=@typescript/typescript-linux-arm64; native_integrity="$TYPESCRIPT_LINUX_ARM64_INTEGRITY" ;;
    *) echo 'unsupported TypeScript architecture' >&2; exit 65 ;;
esac
install_package "$native_package" "$TYPESCRIPT_VERSION" "$native_integrity"
ln -sfn ../lib/node_modules/typescript/bin/tsc /usr/bin/tsc
rm -f /usr/bin/tsserver
test "$(tsc --version | awk '{print $2}')" = "$TYPESCRIPT_VERSION"
test "$(npm --version)" = "$NPM_VERSION"
