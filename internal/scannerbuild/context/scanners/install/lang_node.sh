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

# npm 12.0.2 depends on brace-expansion ^5.0.7 while the fixed 5.0.8+
# release landed after npm's publication. Replace only npm's private copy with
# an integrity-verified 5.0.9 package; eslint/markdownlint already resolve the
# fixed release themselves.
brace_version=5.0.9
brace_integrity='sha512-ScQ4IuvIEF1TMlP7Zt+vjJ//9zlPb2SDcxWxM3bk8s6t6GGdJ7KO1dCcTidOPJKePW30LE/2cT7wCyPho9/Wxg=='
override_tmp="$(mktemp -d)"
trap 'rm -rf "${override_tmp}"' EXIT
(
    cd "${override_tmp}"
    archive="$(npm pack --silent "brace-expansion@${brace_version}")"
    actual="sha512-$(openssl dgst -sha512 -binary "${archive}" | openssl base64 -A)"
    [[ "${actual}" == "${brace_integrity}" ]] || {
        echo "brace-expansion integrity mismatch" >&2
        exit 1
    }
    destination="$(npm root -g)/npm/node_modules/brace-expansion"
    rm -rf "${destination}"
    mkdir -p "${destination}"
    tar -xzf "${archive}" --strip-components=1 -C "${destination}"
)
rm -rf "${override_tmp}"
trap - EXIT

npm cache clean --force >/dev/null 2>&1 || true

echo "Node language scanners installed."
