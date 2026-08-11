#!/usr/bin/env bash
# ============================================================
# Install Node language scanners — eslint. npm-audit is bundled
# with npm itself (no separate install).
# ============================================================
set -euo pipefail

# shellcheck disable=SC1091
source /etc/wolf-scanners/versions.env

# NodeSource includes npm, but the npm release cadence and security fixes are
# independent from Node. Pin and upgrade npm before installing scanner CLIs so
# the final image does not retain vulnerable bundled tar/glob/sigstore modules.
npm install -g --omit=dev --omit=optional "npm@${NPM_VERSION}"
npm install -g --omit=dev --omit=optional \
    "eslint@${ESLINT_VERSION}" \
    "markdownlint-cli@${MARKDOWNLINT_VERSION}"

override_tmp="$(mktemp -d)"
trap 'rm -rf "${override_tmp}"' EXIT

replace_npm_private_package() {
    local package_name="$1"
    local package_version="$2"
    local package_integrity="$3"
    local archive actual destination

    archive="$(cd "${override_tmp}" && npm pack --silent "${package_name}@${package_version}")"
    actual="sha512-$(openssl dgst -sha512 -binary "${override_tmp}/${archive}" | openssl base64 -A)"
    [[ "${actual}" == "${package_integrity}" ]] || {
        echo "${package_name} integrity mismatch" >&2
        exit 1
    }
    destination="$(npm root -g)/npm/node_modules/${package_name}"
    rm -rf "${destination}"
    mkdir -p "${destination}"
    tar -xzf "${override_tmp}/${archive}" --strip-components=1 -C "${destination}"
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

ip_address_version=10.3.1
ip_address_integrity='sha512-1e9d3kb97NHJTIJDZW9rKqW2h6+dFa50Dy0fpPSMQp2ADje5gvKsXmdiK6dwY5t76TaTt5+P5N1Y/LoToIxP6g=='
replace_npm_private_package ip-address "$ip_address_version" "$ip_address_integrity"

rm -rf "${override_tmp}"
trap - EXIT

npm cache clean --force >/dev/null 2>&1 || true

echo "Node language scanners installed."
