#!/usr/bin/env bash
# Create an immutable tag exactly once, then optionally update channel aliases.
# Existing immutable tags are accepted only when they already resolve to the
# requested digest, making retries idempotent without permitting tag mutation.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source_ref="${1:-}"
target_repository="${2:-}"
immutable_tag="${3:-}"
expected_digest="${4:-}"
required_platforms="${5:-}"
aliases="${6:-}"

[[ "$source_ref" == *"@${expected_digest}" ]] ||
    { echo "source reference must be digest-qualified" >&2; exit 2; }
[[ "$immutable_tag" =~ ^[a-z0-9][a-z0-9._-]{0,126}$ ]] ||
    { echo "invalid immutable tag" >&2; exit 2; }

target_ref="${target_repository}:${immutable_tag}"
if docker buildx imagetools inspect "$target_ref" >/dev/null 2>&1; then
    "$script_dir/verify-image.sh" "$target_ref" "$expected_digest" "$required_platforms"
    printf 'immutable ref already exists with the expected digest: %s\n' "$target_ref"
else
    docker buildx imagetools create --tag "$target_ref" "$source_ref"
    "$script_dir/verify-image.sh" "$target_ref" "$expected_digest" "$required_platforms"
fi

IFS=, read -ra alias_values <<<"$aliases"
for alias in "${alias_values[@]}"; do
    [[ -n "$alias" ]] || continue
    [[ "$alias" =~ ^[a-z0-9][a-z0-9._-]{0,126}$ ]] ||
        { printf 'invalid alias: %s\n' "$alias" >&2; exit 2; }
    docker buildx imagetools create \
        --tag "${target_repository}:${alias}" \
        "${target_repository}@${expected_digest}"
    "$script_dir/verify-image.sh" \
        "${target_repository}:${alias}" \
        "$expected_digest" \
        "$required_platforms"
done
