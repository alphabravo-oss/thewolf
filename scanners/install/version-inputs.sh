#!/usr/bin/env bash
# Derive each installer's pins from the canonical file. COPY --from uses the
# resulting file content, so unrelated version bumps retain BuildKit caches.
set -euo pipefail
versions="${1:?versions.env required}"
installers="${2:?installer directory required}"
destination="${3:?output directory required}"
mkdir -p "$destination"
for script in "$installers"/*.sh; do
    name="$(basename "$script" .sh)"
    awk '
        FNR == NR { script = script $0 "\n"; next }
        /^[A-Z][A-Z0-9_]*=/ {
            key = $0; sub(/=.*/, "", key)
            if (script ~ "(^|[^A-Z0-9_])" key "([^A-Z0-9_]|$)") print
        }
    ' "$script" "$versions" > "$destination/$name.env"
done
