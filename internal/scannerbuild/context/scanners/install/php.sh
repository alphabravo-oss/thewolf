#!/usr/bin/env bash
# ============================================================
# Install PHPStan via the official phar release.
# ============================================================
set -euo pipefail

# shellcheck disable=SC1091
source /etc/wolf-scanners/versions.env

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
curl --proto '=https' --tlsv1.2 --fail --location --silent --show-error \
    --proto-redir '=https' --retry 3 --retry-all-errors \
    "https://github.com/phpstan/phpstan/releases/download/${PHPSTAN_VERSION}/phpstan.phar" \
    -o "${tmp}/phpstan.phar"
printf '%s  %s\n' "$PHPSTAN_PHAR_SHA256" "${tmp}/phpstan.phar" \
    | sha256sum --check --strict -
install -m 0755 "${tmp}/phpstan.phar" /usr/local/bin/phpstan
rm -rf "$tmp"
trap - EXIT
echo "PHPStan installed."
