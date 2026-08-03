#!/usr/bin/env bash
set -euo pipefail

[[ "${WOLF_RUN_SCANNER_KIND_E2E:-}" == "1" ]] || {
  echo "SKIP: set WOLF_RUN_SCANNER_KIND_E2E=1 to run the real scanner gate"
  exit 0
}
image="${WOLF_SCANNER_E2E_IMAGE:-}"
qualification_dir="${WOLF_RELEASE_QUALIFICATION_DIR:-/usr/local/libexec/wolf/release-qualification}"
[[ "$image" =~ ^[^[:space:]@]+@sha256:[a-f0-9]{64}$ ]] || {
  echo "WOLF_SCANNER_E2E_IMAGE must be an exact repository@sha256 reference" >&2
  exit 2
}
for command in docker kind kubectl jq python3; do
  command -v "$command" >/dev/null || {
    echo "required command is unavailable: $command" >&2
    exit 2
  }
done
[[ -x "$qualification_dir/python-parser-qualification.test" ]] || {
  echo "trusted Python parser qualification binary is unavailable in $qualification_dir" >&2
  exit 2
}

now_ms() {
  python3 -c 'import time; print(time.time_ns() // 1_000_000)'
}

work="$(mktemp -d "${TMPDIR:-/tmp}/wolf-scanner-kind.XXXXXX")"
cluster="wolf-scanner-quality-${RANDOM}-$$"
context="kind-$cluster"
namespace="wolf-scanner-quality"
kubeconfig="$work/kubeconfig"
registry_container="${WOLF_SCANNER_E2E_REGISTRY_CONTAINER:-}"
registry_endpoint="${WOLF_SCANNER_E2E_REGISTRY_ENDPOINT:-}"
registry_connected=0
: "${WOLF_KIND_API_ADDRESS:?reachable remote Kind API address is required}"
: "${WOLF_KIND_QUALITY_API_PORT:?remote Kind quality API port is required}"
case "$WOLF_KIND_API_ADDRESS:$WOLF_KIND_QUALITY_API_PORT" in
  *[!0-9.:]*) echo "remote Kind API address/port is invalid" >&2; exit 2 ;;
esac
cleanup() {
  if [[ "$registry_connected" == "1" ]]; then
    docker network disconnect kind "$registry_container" >/dev/null 2>&1 || true
  fi
  kind delete cluster --name "$cluster" >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

cat >"$work/kind.yaml" <<YAML
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
networking:
  apiServerAddress: "$WOLF_KIND_API_ADDRESS"
  apiServerPort: $WOLF_KIND_QUALITY_API_PORT
YAML
if [[ -n "$registry_container" || -n "$registry_endpoint" ]]; then
  cat >>"$work/kind.yaml" <<'YAML'
containerdConfigPatches:
  - |-
    [plugins."io.containerd.grpc.v1.cri".registry]
      config_path = "/etc/containerd/certs.d"
YAML
fi
kind create cluster --name "$cluster" --wait 120s --kubeconfig "$kubeconfig" \
  --config "$work/kind.yaml"
node="${cluster}-control-plane"

# An opt-in registry mirror keeps local qualification entirely local while the
# Pod still requests and records the exact repository@sha256 identity. The
# default path remains an ordinary remote registry pull.
if [[ -n "$registry_container" || -n "$registry_endpoint" ]]; then
  [[ -n "$registry_container" && "$registry_endpoint" =~ ^https?://[^[:space:]]+$ ]] || {
    echo "local registry qualification requires both WOLF_SCANNER_E2E_REGISTRY_CONTAINER and a valid WOLF_SCANNER_E2E_REGISTRY_ENDPOINT" >&2
    exit 2
  }
  registry_host="${image%%/*}"
  [[ "$registry_host" != "$image" ]] || {
    echo "local registry qualification requires an explicit registry host in WOLF_SCANNER_E2E_IMAGE" >&2
    exit 2
  }
  docker inspect "$registry_container" >/dev/null
  docker network connect kind "$registry_container"
  registry_connected=1
  docker exec "$node" mkdir -p "/etc/containerd/certs.d/${registry_host}"
  printf '[host."%s"]\n  capabilities = ["pull", "resolve"]\n' \
    "$registry_endpoint" \
    | docker exec -i "$node" sh -c "cat >'/etc/containerd/certs.d/${registry_host}/hosts.toml'"
fi

pull_started_ms="$(now_ms)"
docker exec "$node" crictl pull "$image"
pull_duration_ms="$(( $(now_ms) - pull_started_ms ))"
kubectl --kubeconfig "$kubeconfig" --context "$context" create namespace "$namespace"
cat >"$work/main.py" <<'PYTHON'
import subprocess
subprocess.call("printf fixture", shell=True)
PYTHON
kubectl --kubeconfig "$kubeconfig" --context "$context" -n "$namespace" \
  create configmap scanner-source --from-file=main.py="$work/main.py"
cat >"$work/job.yaml" <<YAML
apiVersion: batch/v1
kind: Job
metadata:
  name: wolf-bandit-quality
  namespace: $namespace
spec:
  backoffLimit: 0
  ttlSecondsAfterFinished: 60
  template:
    spec:
      restartPolicy: Never
      automountServiceAccountToken: false
      securityContext:
        runAsNonRoot: true
        # The scanner image declares USER wolf. Kubernetes requires numeric
        # identity here to verify the named image user cannot resolve to root.
        runAsUser: 1000
        runAsGroup: 1000
        seccompProfile: {type: RuntimeDefault}
      containers:
        - name: scanner
          image: $image
          # The exact digest was pulled into this disposable node above. Keep
          # scanner execution time separate from registry transfer time.
          imagePullPolicy: IfNotPresent
          args: [bandit, -r, /fixture, -f, json, --exit-zero]
          securityContext:
            allowPrivilegeEscalation: false
            capabilities: {drop: [ALL]}
            readOnlyRootFilesystem: true
          volumeMounts:
            - {name: source, mountPath: /fixture, readOnly: true}
      volumes:
        - name: source
          configMap: {name: scanner-source}
YAML
kubectl --kubeconfig "$kubeconfig" --context "$context" create --filename "$work/job.yaml"
started_ms="$(now_ms)"
if ! kubectl --kubeconfig "$kubeconfig" --context "$context" -n "$namespace" \
  wait --for=condition=complete job/wolf-bandit-quality --timeout=300s; then
  kubectl --kubeconfig "$kubeconfig" --context "$context" -n "$namespace" \
    describe job/wolf-bandit-quality >&2 || true
  kubectl --kubeconfig "$kubeconfig" --context "$context" -n "$namespace" \
    get pods -o wide >&2 || true
  kubectl --kubeconfig "$kubeconfig" --context "$context" -n "$namespace" \
    get events --sort-by=.lastTimestamp >&2 || true
  exit 1
fi
duration_ms="$(( $(now_ms) - started_ms ))"
pod="$(kubectl --kubeconfig "$kubeconfig" --context "$context" -n "$namespace" \
  get pod -l job-name=wolf-bandit-quality -o jsonpath='{.items[0].metadata.name}')"
requested="$(kubectl --kubeconfig "$kubeconfig" --context "$context" -n "$namespace" \
  get pod "$pod" -o jsonpath='{.spec.containers[0].image}')"
image_id="$(kubectl --kubeconfig "$kubeconfig" --context "$context" -n "$namespace" \
  get pod "$pod" -o jsonpath='{.status.containerStatuses[0].imageID}')"
expected_digest="${image##*@}"
[[ "$requested" == "$image" ]] || {
  echo "Kind Pod did not retain the exact requested reference" >&2
  exit 1
}
[[ "$image_id" == *"@$expected_digest" || "$image_id" == *"://$expected_digest" ]] || {
  echo "Kind imageID digest mismatch: $image_id" >&2
  exit 1
}
kubectl --kubeconfig "$kubeconfig" --context "$context" -n "$namespace" \
  logs "$pod" >"$work/bandit.json"
# Kubernetes exposes the merged stdout/stderr stream. Validate that the real
# Wolf parser can skip Bandit's bracketed INFO preamble and normalize findings.
WOLF_BANDIT_E2E_OUTPUT="$work/bandit.json" \
  "$qualification_dir/python-parser-qualification.test" \
  -test.run '^TestParseBanditRealE2EOutput$' -test.count=1

jq -n \
  --arg image "$image" \
  --arg imageId "$image_id" \
  --argjson pullDurationMs "$pull_duration_ms" \
  --argjson durationMs "$duration_ms" \
  --argjson outputBytes "$(wc -c <"$work/bandit.json" | tr -d ' ')" \
  '{
    schemaVersion: "wolf.scanners/integration-evidence/v1",
    runtime: "kind",
    tool: "bandit",
    image: $image,
    imageId: $imageId,
    mergedRuntimeLog: true,
    pullDurationMs: $pullDurationMs,
    durationMs: $durationMs,
    outputBytes: $outputBytes,
    parser: "wolf/plugin/bandit",
    result: "passed"
  }'
