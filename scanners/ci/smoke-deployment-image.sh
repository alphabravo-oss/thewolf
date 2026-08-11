#!/usr/bin/env bash
# Execute the non-networked, architecture-native contract for a published
# deployment/control-plane image child manifest.
set -euo pipefail

image="${1:-}"
platform="${2:-}"
variant="${3:-}"

[[ "$image" =~ ^[^[:space:]]+@sha256:[a-f0-9]{64}$ ]] || {
    echo "deployment image must be digest-qualified" >&2
    exit 2
}
[[ "$platform" =~ ^linux/(amd64|arm64)$ ]] || {
    echo "unsupported deployment image platform: $platform" >&2
    exit 2
}
[[ "$variant" =~ ^(runtime|proposal|release-fixed|release-quality|release-integration)$ ]] || {
    echo "unsupported deployment image variant: $variant" >&2
    exit 2
}

test "$(docker image inspect "$image" --format '{{.Config.User}}')" = wolf
test "$(docker image inspect "$image" --format '{{index .Config.Labels "org.opencontainers.image.source"}}')" = \
    https://github.com/alphabravo-oss/thewolf
docker run --rm --platform "$platform" --network none \
    --entrypoint /bin/sh "$image" -ec 'test "$(id -u)" -ne 0'

case "$variant" in
    runtime)
        docker run --rm --platform "$platform" --network none \
            --entrypoint wolf "$image" version
        docker run --rm --platform "$platform" --network none \
            --entrypoint /bin/sh "$image" -ec '
              test -s /usr/share/wolf/scanners/scanner-lock.yaml
              test -s /usr/share/wolf/scanners/tools.yaml
              test -s /usr/share/wolf/ui/dist/index.html
              test -x /usr/libexec/docker/cli-plugins/docker-buildx
            '
        ;;
    proposal)
        docker run --rm --platform "$platform" --network none \
            --entrypoint wolf "$image" version
        test "$(docker image inspect "$image" --format '{{json .Config.Cmd}}')" = \
            '["scanner-release-worker","--role=proposal"]'
        ;;
    release-fixed)
        docker run --rm --platform "$platform" --network none \
            --entrypoint /bin/sh "$image" -ec '
              test -x /usr/local/bin/wolf-release-adapter
              test -x /usr/local/bin/scannertools
              test -x /usr/local/bin/synccontext
              test -x /usr/local/bin/oras
              test -x /usr/local/go/bin/go
            '
        ;;
    release-quality)
        docker run --rm --platform "$platform" --network none \
            --entrypoint /bin/sh "$image" -ec '
              test -x /usr/local/bin/wolf-release-adapter
              test -x /usr/local/bin/scannertools
              test -x /usr/local/bin/oras
              test -x /usr/local/bin/trivy
              trivy --version | grep -F "Version: 0.73.0"
            '
        ;;
    release-integration)
        docker run --rm --platform "$platform" --network none \
            --entrypoint /bin/sh "$image" -ec '
              test -x /usr/local/bin/wolf-release-adapter
              test -x /usr/local/bin/oras
              test -x /usr/local/libexec/wolf/release-qualification/python-parser-qualification.test
              test -x /usr/local/libexec/wolf/release-qualification/scanner-rollout-qualification.test
              kind version
              kubectl version --client=true
              docker compose version
            '
        ;;
esac

printf 'deployment image smoke passed: %s (%s, %s)\n' "$image" "$platform" "$variant"
