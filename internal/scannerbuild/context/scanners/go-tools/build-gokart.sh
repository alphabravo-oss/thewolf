#!/usr/bin/env bash
# Build the pinned GoKart with its x/tools driver compatibility patch. Keep
# upstream's module cache and our security-maintained module graph unchanged.
set -euo pipefail
root="$(cd "$(dirname "$0")" && pwd)"
output="${1:?absolute output binary path required}"
[[ "$output" = /* ]] || { echo 'output path must be absolute' >&2; exit 2; }
cd "$root"
go mod download github.com/praetorian-inc/gokart
source_dir="$(go list -mod=readonly -m -f '{{.Dir}}' github.com/praetorian-inc/gokart)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
cp -R "$source_dir" "$work/gokart"
chmod -R u+w "$work/gokart"
cp go.mod go.sum "$work/"
cd "$work"
git apply --directory gokart "$root/gokart.patch"
# A relative replacement keeps temporary directory names out of build metadata.
go mod edit -replace github.com/praetorian-inc/gokart=./gokart
go build -mod=readonly -trimpath -o "$output" github.com/praetorian-inc/gokart
