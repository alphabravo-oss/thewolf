#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
cp "$root/scanners/versions.env" "$work/versions.env"
generate() { bash "$root/scanners/install/version-inputs.sh" "$work/versions.env" "$root/scanners/install" "$1"; }
generate "$work/before"
sed -i 's/^SEMGREP_VERSION=.*/SEMGREP_VERSION=999.0.0/' "$work/versions.env"
generate "$work/upstream"
for installer in lang_python core_python go-tools lang_node ruby php rust jvm codeql; do
    cmp "$work/before/$installer.env" "$work/upstream/$installer.env"
done
sed -i 's/^BANDIT_VERSION=.*/BANDIT_VERSION=999.0.0/' "$work/versions.env"
generate "$work/python"
if cmp -s "$work/before/lang_python.env" "$work/python/lang_python.env"; then
    echo 'bandit update did not invalidate its installer input' >&2; exit 1
fi
for installer in core_python go-tools lang_node ruby php rust jvm codeql; do
    cmp "$work/before/$installer.env" "$work/python/$installer.env"
done
grep -q '^PHPSTAN_PHAR_SHA256=' "$work/before/php.env"
grep -q '^RUSTUP_LINUX_ARM64_SHA256=' "$work/before/rust.env"
echo 'installer cache isolation and checksum inputs OK'
