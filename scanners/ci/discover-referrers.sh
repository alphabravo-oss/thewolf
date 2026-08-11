#!/usr/bin/env bash
set -euo pipefail

reference="${1:-}"
digest="${2:-}"
output="${3:-}"
error_message="${4:-supply-chain referrer set is incomplete}"

[[ -n "$reference" ]] || { echo "image reference is required" >&2; exit 2; }
[[ "$digest" =~ ^sha256:[a-f0-9]{64}$ ]] || { echo "valid subject digest is required" >&2; exit 2; }
[[ -n "$output" ]] || { echo "output path is required" >&2; exit 2; }

attempts="${WOLF_REFERRER_DISCOVERY_ATTEMPTS:-60}"
sleep_seconds="${WOLF_REFERRER_DISCOVERY_SLEEP_SECONDS:-10}"
[[ "$attempts" =~ ^[1-9][0-9]*$ ]] || { echo "retry attempts must be a positive integer" >&2; exit 2; }
[[ "$sleep_seconds" =~ ^[0-9]+$ ]] || { echo "retry sleep must be a non-negative integer" >&2; exit 2; }

mkdir -p "$(dirname "$output")"
raw="${output}.raw"

for ((attempt = 1; attempt <= attempts; attempt++)); do
    if oras discover --format json "$reference" >"$raw" \
        && jq -e '
            (.referrers | type) == "array"
            and (.referrers | length) >= 3
            and all(.referrers[]; (.digest | test("^sha256:[a-f0-9]{64}$")))
          ' "$raw" >/dev/null; then
        jq -S --arg digest "$digest" '{
          subjectDigest: $digest,
          referrers: (.referrers | sort_by(.artifactType, .digest))
        }' "$raw" >"$output"
        rm -f "$raw"
        exit 0
    fi

    if ((attempt < attempts)); then
        echo "referrer inventory incomplete for $reference; retrying ($attempt/$attempts)" >&2
        sleep "$sleep_seconds"
    fi
done

jq -S --arg digest "$digest" --arg message "$error_message" '
    if (.referrers | type) != "array" or (.referrers | length) < 3 or
       any(.referrers[]; (.digest | test("^sha256:[a-f0-9]{64}$") | not))
    then error($message)
    else {subjectDigest: $digest, referrers: (.referrers | sort_by(.artifactType, .digest))}
    end
  ' "$raw" >"$output"
