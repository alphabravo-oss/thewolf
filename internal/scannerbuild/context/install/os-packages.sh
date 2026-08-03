#!/usr/bin/env bash
# Install the direct operating-system packages selected by
# scanners/os-packages.lock.yaml.
#
# The Debian sources point at one immutable snapshot timestamp. Every direct
# package is installed at its exact locked version and architecture. Packages
# from external apt repositories are downloaded as exact .deb artifacts and
# verified by SHA-256 before apt is allowed to install them.
set -euo pipefail

variant="${1:-}"
case "$variant" in
    codeql|default|fixer-base|jvm|min|rust) ;;
    *)
        echo "usage: install-os-packages {codeql|default|fixer-base|jvm|min|rust}" >&2
        exit 64
        ;;
esac

architecture="$(dpkg --print-architecture)"
case "$architecture" in
    amd64|arm64) ;;
    *)
        echo "unsupported scanner image architecture: $architecture" >&2
        exit 65
        ;;
esac

lock_root="/etc/wolf-scanners/os-packages"
sources_file="$lock_root/snapshot.sources"
pins_file="$lock_root/pins/${variant}-${architecture}.txt"
artifacts_file="$lock_root/artifacts/${variant}-${architecture}.txt"
bootstrap_package="$lock_root/bootstrap/ca-certificates.deb"
bootstrap_checksum="$lock_root/bootstrap/ca-certificates.sha256"

for required_file in \
    "$sources_file" "$pins_file" "$artifacts_file" \
    "$bootstrap_package" "$bootstrap_checksum"; do
    if [[ ! -f "$required_file" ]]; then
        echo "locked OS package input is missing: $required_file" >&2
        exit 66
    fi
done

# debian:trixie-slim omits the configured CA bundle. Bootstrap it from the
# exact ca-certificates .deb fetched and checksum-verified by the explicit lock
# refresh operation. The package is only extracted here; apt installs and
# configures the same exact pin normally after HTTPS metadata is available.
(
    cd "$(dirname "$bootstrap_package")"
    sha256sum --check --strict "$(basename "$bootstrap_checksum")"
)
bootstrap_root="$(mktemp -d /tmp/wolf-ca-bootstrap.XXXXXX)"
cleanup_bootstrap() {
    rm -rf -- "$bootstrap_root"
}
trap cleanup_bootstrap EXIT
dpkg-deb --extract "$bootstrap_package" "$bootstrap_root"
install -d -m 0755 /etc/ssl/certs
find "$bootstrap_root/usr/share/ca-certificates/mozilla" -type f -name '*.crt' -print0 \
    | sort -z \
    | xargs -0 cat > /etc/ssl/certs/ca-certificates.crt
[[ -s /etc/ssl/certs/ca-certificates.crt ]] || {
    echo "ca-certificates bootstrap produced an empty trust bundle" >&2
    exit 69
}

# Replace the base image's moving Debian mirrors before apt refreshes any
# HTTPS index. Check-Valid-Until is intentionally disabled because immutable
# snapshots must remain installable after their Release metadata expires.
install -m 0644 "$sources_file" /etc/apt/sources.list.d/wolf-snapshot.sources
rm -f /etc/apt/sources.list /etc/apt/sources.list.d/debian.sources
printf '%s\n' \
    'Acquire::Check-Valid-Until "false";' \
    'Acquire::https::CaInfo "/etc/ssl/certs/ca-certificates.crt";' \
    > /etc/apt/apt.conf.d/99wolf-snapshot

apt-get update

declare -a package_pins=()
while IFS= read -r package_pin || [[ -n "$package_pin" ]]; do
    [[ -z "$package_pin" || "$package_pin" == \#* ]] && continue
    if [[ "$package_pin" =~ [[:space:]] ]]; then
        echo "invalid whitespace in OS package pin: $package_pin" >&2
        exit 67
    fi
    package_pins+=("$package_pin")
done < "$pins_file"

if ((${#package_pins[@]} > 0)); then
    apt-get install -y --no-install-recommends --allow-downgrades \
        "${package_pins[@]}"
fi
cleanup_bootstrap
trap - EXIT

artifact_dir="$(mktemp -d /tmp/wolf-os-packages.XXXXXX)"
cleanup_artifacts() {
    rm -rf -- "$artifact_dir"
}
trap cleanup_artifacts EXIT

declare -a artifact_packages=()
while IFS=$'\t' read -r package_name package_url package_sha256 package_filename ||
    [[ -n "$package_name" ]]; do
    [[ -z "$package_name" || "$package_name" == \#* ]] && continue
    if [[ ! "$package_name" =~ ^[a-z0-9][a-z0-9+.-]*$ ]] ||
        [[ ! "$package_url" =~ ^https:// ]] ||
        [[ ! "$package_sha256" =~ ^[a-f0-9]{64}$ ]] ||
        [[ ! "$package_filename" =~ ^[A-Za-z0-9][A-Za-z0-9._+-]*\.deb$ ]]; then
        echo "invalid locked external package record for $package_name" >&2
        exit 68
    fi
    destination="$artifact_dir/$package_filename"
    curl --proto '=https' --tlsv1.2 --fail --location --silent --show-error \
        --proto-redir '=https' \
        --retry 3 --retry-all-errors \
        --output "$destination" "$package_url"
    printf '%s  %s\n' "$package_sha256" "$destination" | sha256sum --check --strict -
    artifact_packages+=("$destination")
done < "$artifacts_file"

if ((${#artifact_packages[@]} > 0)); then
    # Dependencies are resolved only from the immutable Debian snapshot above.
    apt-get install -y --no-install-recommends --allow-downgrades \
        "${artifact_packages[@]}"
fi

apt-get clean
rm -rf /var/lib/apt/lists/* /var/cache/apt/*
