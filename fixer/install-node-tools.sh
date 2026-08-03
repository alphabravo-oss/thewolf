#!/usr/bin/env bash
set -euo pipefail

versions_file="${1:-/usr/share/wolf/fixer/versions.env}"
# shellcheck disable=SC1090
source "$versions_file"

actual_integrity="$(npm view "typescript@${TYPESCRIPT_VERSION}" dist.integrity)"
test "$actual_integrity" = "$TYPESCRIPT_INTEGRITY"

# NodeSource's bundled npm is intentionally replaced because npm's security
# release cadence is independent of Node's. Stage the exact npm tarball and
# replace the complete directory instead of installing over the distribution
# copy: an in-place global upgrade can retain stale nested dependencies.
override_tmp="$(mktemp -d)"
trap 'rm -rf "${override_tmp}"' EXIT
global_root="$(npm root --global)"
npm_archive="$(cd "$override_tmp" && npm pack --silent "npm@${NPM_VERSION}")"
actual_integrity="sha512-$(openssl dgst -sha512 -binary "${override_tmp}/${npm_archive}" | openssl base64 -A)"
test "$actual_integrity" = "$NPM_INTEGRITY"

typescript_archive="$(cd "$override_tmp" && npm pack --silent "typescript@${TYPESCRIPT_VERSION}")"
actual_integrity="sha512-$(openssl dgst -sha512 -binary "${override_tmp}/${typescript_archive}" | openssl base64 -A)"
test "$actual_integrity" = "$TYPESCRIPT_INTEGRITY"

rm -rf "${global_root}/npm"
mkdir -p "${global_root}/npm"
tar -xzf "${override_tmp}/${npm_archive}" --strip-components=1 -C "${global_root}/npm"
test "$(npm --version)" = "$NPM_VERSION"

# Extract TypeScript's verified package into a clean global directory. Running
# a second global reify after installing npm can mutate npm's bundled dependency
# graph, so global tools are never installed over that graph in place.
rm -rf "${global_root}/typescript"
mkdir -p "${global_root}/typescript"
tar -xzf "${override_tmp}/${typescript_archive}" --strip-components=1 -C "${global_root}/typescript"
ln -sfn ../lib/node_modules/typescript/bin/tsc /usr/bin/tsc
ln -sfn ../lib/node_modules/typescript/bin/tsserver /usr/bin/tsserver
test "$(tsc --version | awk '{print $2}')" = "$TYPESCRIPT_VERSION"

replace_npm_private_package() {
    local package_name="$1"
    local package_version="$2"
    local package_integrity="$3"
    local archive actual destination

    archive="$(cd "$override_tmp" && npm pack --silent "${package_name}@${package_version}")"
    actual="sha512-$(openssl dgst -sha512 -binary "${override_tmp}/${archive}" | openssl base64 -A)"
    test "$actual" = "$package_integrity"
    destination="$(npm root -g)/npm/node_modules/${package_name}"
    rm -rf "$destination"
    mkdir -p "$destination"
    tar -xzf "${override_tmp}/${archive}" --strip-components=1 -C "$destination"
}

# npm 12.0.2 was published before these patched transitive releases. Replace
# only npm's private copies with registry-integrity-verified fixed packages
# until npm incorporates the releases directly.
brace_version=5.0.9
brace_integrity='sha512-ScQ4IuvIEF1TMlP7Zt+vjJ//9zlPb2SDcxWxM3bk8s6t6GGdJ7KO1dCcTidOPJKePW30LE/2cT7wCyPho9/Wxg=='
replace_npm_private_package brace-expansion "$brace_version" "$brace_integrity"

picomatch_version=4.0.5
picomatch_integrity='sha512-RvwwcruNjI1ncT5xRakeyS9Lf8lcItv34KD+aif+VH9kduAyfYBipGh12274xtenIPZ119/R9BdTBa8gAwSh0A=='
replace_npm_private_package picomatch "$picomatch_version" "$picomatch_integrity"

rm -rf "$override_tmp"
trap - EXIT

test "$(node -p "require('$(npm root -g)/npm/package.json').version")" = "$NPM_VERSION"
test "$(node -p "require('$(npm root -g)/npm/node_modules/brace-expansion/package.json').version")" = "$brace_version"
test "$(node -p "require('$(npm root -g)/npm/node_modules/picomatch/package.json').version")" = "$picomatch_version"
npm cache clean --force >/dev/null 2>&1
test "$(npm view "npm@${NPM_VERSION}" version)" = "$NPM_VERSION"
