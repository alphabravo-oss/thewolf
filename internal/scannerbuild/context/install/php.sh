#!/usr/bin/env bash
# ============================================================
# Install PHPStan via the official phar release.
# ============================================================
set -uo pipefail

# shellcheck disable=SC1091
source /etc/wolf-scanners/versions.env

if curl -fsSL -o /usr/local/bin/phpstan \
    "https://github.com/phpstan/phpstan/releases/download/${PHPSTAN_VERSION}/phpstan.phar"; then
    chmod +x /usr/local/bin/phpstan
    echo "PHPStan installed."
else
    echo "WARNING: phpstan download failed; skipping"
    rm -f /usr/local/bin/phpstan
fi
