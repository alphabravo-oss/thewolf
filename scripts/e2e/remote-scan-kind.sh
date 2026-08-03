#!/usr/bin/env bash
set -euo pipefail

[[ "${WOLF_RUN_REMOTE_SCAN_KIND_E2E:-}" == "1" ]] || {
  echo "SKIP: set WOLF_RUN_REMOTE_SCAN_KIND_E2E=1 to run Kind qualification"
  exit 0
}

runtime_image="${WOLF_E2E_RUNTIME_IMAGE:-}"
scanner_image="${WOLF_E2E_SCANNER_IMAGE:-}"
postgres_image="${WOLF_E2E_POSTGRES_IMAGE:-}"
timeout_seconds="${WOLF_E2E_TIMEOUT_SECONDS:-1200}"
load_local_images="${WOLF_E2E_KIND_LOAD_LOCAL_IMAGES:-0}"
memory_pvs="${WOLF_E2E_KIND_MEMORY_PVS:-0}"
registry_container="${WOLF_E2E_KIND_REGISTRY_CONTAINER:-}"
registry_endpoint="${WOLF_E2E_KIND_REGISTRY_ENDPOINT:-}"
for value_name in runtime_image scanner_image postgres_image; do
  value="${!value_name}"
  [[ "$value" =~ ^[^[:space:]@]+@sha256:[a-f0-9]{64}$ ]] || {
    echo "$value_name must be an exact repository@sha256 reference" >&2
    exit 2
  }
done
[[ "$load_local_images" == 0 || "$load_local_images" == 1 ]] || {
  echo "WOLF_E2E_KIND_LOAD_LOCAL_IMAGES must be 0 or 1" >&2
  exit 2
}
[[ "$memory_pvs" == 0 || "$memory_pvs" == 1 ]] || {
  echo "WOLF_E2E_KIND_MEMORY_PVS must be 0 or 1" >&2
  exit 2
}
for command in curl docker git helm jq kind kubectl python3; do
  command -v "$command" >/dev/null || {
    echo "required command is unavailable: $command" >&2
    exit 2
  }
done

project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
probe="$project_root/scripts/e2e/remote-scan-api-smoke.sh"
chart="$project_root/deploy/helm/wolf"
work="$(mktemp -d "${TMPDIR:-/tmp}/wolf-kind-deployment.XXXXXX")"
cluster="wolf-remote-scan-${RANDOM}-$$"
context="kind-$cluster"
namespace="wolf-qualification"
release="qualification"
kubeconfig="$work/kubeconfig"
forward_pid=""
registry_connected=0
cleanup() {
  if [[ -n "$forward_pid" ]]; then
    kill -TERM "$forward_pid" >/dev/null 2>&1 || true
    wait "$forward_pid" >/dev/null 2>&1 || true
  fi
  if [[ "$registry_connected" == 1 ]]; then
    docker network disconnect kind "$registry_container" >/dev/null 2>&1 || true
  fi
  kind delete cluster --name "$cluster" >/dev/null 2>&1 || true
  find "$work" -mindepth 1 -delete >/dev/null 2>&1 || true
  rmdir "$work" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

api_address="${WOLF_KIND_API_ADDRESS:-127.0.0.1}"
api_port="${WOLF_KIND_REMOTE_SCAN_API_PORT:-}"
if [[ -z "$api_port" ]]; then
  api_port="$(python3 - <<'PY'
import socket
with socket.socket() as sock:
    sock.bind(("127.0.0.1", 0))
    print(sock.getsockname()[1])
PY
)"
fi
[[ "$api_address" =~ ^[0-9a-fA-F:.]+$ && "$api_port" =~ ^[1-9][0-9]{1,4}$ ]] || {
  echo "Kind API address or port is invalid" >&2
  exit 2
}
{
  printf '%s\n' \
    'kind: Cluster' \
    'apiVersion: kind.x-k8s.io/v1alpha4' \
    'networking:' \
    "  apiServerAddress: '$api_address'" \
    "  apiServerPort: $api_port"
} >"$work/kind.yml"
if [[ -n "$registry_container" || -n "$registry_endpoint" ]]; then
  {
    printf '%s\n' \
      'containerdConfigPatches:' \
      '  - |-' \
      '    [plugins."io.containerd.grpc.v1.cri".registry]' \
      '      config_path = "/etc/containerd/certs.d"'
  } >>"$work/kind.yml"
fi
kind create cluster --name "$cluster" --config "$work/kind.yml" \
  --kubeconfig "$kubeconfig" --wait 180s
node="${cluster}-control-plane"

if [[ -n "$registry_container" || -n "$registry_endpoint" ]]; then
  [[ -n "$registry_container" && "$registry_endpoint" =~ ^https?://[^[:space:]]+$ ]] || {
    echo "Kind registry mapping requires both container and endpoint" >&2
    exit 2
  }
  docker inspect "$registry_container" >/dev/null
  docker network connect kind "$registry_container"
  registry_connected=1
  declare -A registry_hosts=()
  for image in "$runtime_image" "$scanner_image" "$postgres_image"; do
    registry_hosts["${image%%/*}"]=1
  done
  for registry_host in "${!registry_hosts[@]}"; do
    docker exec "$node" mkdir -p "/etc/containerd/certs.d/$registry_host"
    printf '[host."%s"]\n  capabilities = ["pull", "resolve"]\n' \
      "$registry_endpoint" \
      | docker exec -i "$node" sh -c \
        "cat >'/etc/containerd/certs.d/$registry_host/hosts.toml'"
  done
fi

if [[ "$load_local_images" == 1 ]]; then
  for image in "$runtime_image" "$scanner_image" "$postgres_image"; do
    docker image inspect "$image" >/dev/null
    kind load docker-image --name "$cluster" "$image"
  done
fi

master_key="$(python3 - <<'PY'
import secrets
print(secrets.token_hex(32))
PY
)"
postgres_password="$(python3 - <<'PY'
import secrets
print(secrets.token_urlsafe(24))
PY
)"
runtime_repository="${runtime_image%@*}"
runtime_digest="${runtime_image##*@}"
postgres_repository="${postgres_image%@*}"
postgres_digest="${postgres_image##*@}"

storage_args=()
if [[ "$memory_pvs" == 1 ]]; then
  # These static volumes still exercise PVC binding and survive pod restarts,
  # while keeping a disposable qualification cluster isolated from unrelated
  # container-engine disk on long-lived developer machines.
  docker exec "$node" sh -ec '
    mkdir -p /run/wolf-qualification/workspace \
      /run/wolf-qualification/artifacts \
      /run/wolf-qualification/postgres
    chown 1000:1000 /run/wolf-qualification/workspace \
      /run/wolf-qualification/artifacts
    chmod 0770 /run/wolf-qualification/workspace \
      /run/wolf-qualification/artifacts
    chown 70:70 /run/wolf-qualification/postgres
    chmod 0700 /run/wolf-qualification/postgres
  '
  kubectl --kubeconfig "$kubeconfig" --context "$context" create namespace "$namespace"
  kubectl --kubeconfig "$kubeconfig" --context "$context" apply -f - <<YAML
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata: {name: wolf-qualification-memory}
provisioner: kubernetes.io/no-provisioner
volumeBindingMode: Immediate
---
apiVersion: v1
kind: PersistentVolume
metadata: {name: wolf-qualification-workspace}
spec:
  storageClassName: wolf-qualification-memory
  persistentVolumeReclaimPolicy: Delete
  capacity: {storage: 1Gi}
  accessModes: [ReadWriteOnce]
  hostPath: {path: /run/wolf-qualification/workspace, type: DirectoryOrCreate}
  claimRef: {namespace: $namespace, name: $release-wolf-workspace}
---
apiVersion: v1
kind: PersistentVolume
metadata: {name: wolf-qualification-artifacts}
spec:
  storageClassName: wolf-qualification-memory
  persistentVolumeReclaimPolicy: Delete
  capacity: {storage: 1Gi}
  accessModes: [ReadWriteOnce]
  hostPath: {path: /run/wolf-qualification/artifacts, type: DirectoryOrCreate}
  claimRef: {namespace: $namespace, name: $release-wolf-artifacts}
---
apiVersion: v1
kind: PersistentVolume
metadata: {name: wolf-qualification-postgres}
spec:
  storageClassName: wolf-qualification-memory
  persistentVolumeReclaimPolicy: Delete
  capacity: {storage: 1Gi}
  accessModes: [ReadWriteOnce]
  hostPath: {path: /run/wolf-qualification/postgres, type: DirectoryOrCreate}
  claimRef: {namespace: $namespace, name: $release-wolf-postgres}
YAML
  storage_args=(
    --set-string workspace.storageClassName=wolf-qualification-memory
    --set-string artifacts.storageClassName=wolf-qualification-memory
    --set-string postgres.storageClassName=wolf-qualification-memory
    --set postgres.storage=1Gi
  )
fi

helm --kubeconfig "$kubeconfig" --kube-context "$context" upgrade --install \
  "$release" "$chart" --namespace "$namespace" --create-namespace --wait \
  --timeout 10m \
  --set-string "image.repository=$runtime_repository" \
  --set-string "image.digest=$runtime_digest" \
  --set image.pullPolicy=IfNotPresent \
  --set-string "postgres.image=$postgres_repository" \
  --set-string "postgres.digest=$postgres_digest" \
  --set-string "postgres.password=$postgres_password" \
  --set-string "masterKey=$master_key" \
  --set apiOnly=true \
  --set scannerRelease.mode=read_only \
  --set-string "scanner.defaultImage=$scanner_image" \
  --set-string "scanner.jvmImage=$scanner_image" \
  --set-string "scanner.rustImage=$scanner_image" \
  --set-string scanner.codeqlImage= \
  --set scanner.imagePullPolicy=IfNotPresent \
  --set 'workspace.accessModes={ReadWriteOnce}' \
  --set workspace.size=1Gi \
  --set 'artifacts.accessModes={ReadWriteOnce}' \
  --set artifacts.size=1Gi \
  "${storage_args[@]}" \
  --set networkPolicy.enabled=false

api_deployment="${release}-wolf-api"
worker_deployment="${release}-wolf-scan-worker"
kubectl --kubeconfig "$kubeconfig" --context "$context" --namespace "$namespace" \
  rollout status "deployment/$api_deployment" --timeout=300s
kubectl --kubeconfig "$kubeconfig" --context "$context" --namespace "$namespace" \
  rollout status "deployment/$worker_deployment" --timeout=300s

# Populate the shared worker workspace without adding a qualification-only
# hostPath or image. The actual scan uses the same PVC and Kubernetes Job path
# as customer workloads.
worker_pod="$(kubectl --kubeconfig "$kubeconfig" --context "$context" \
  --namespace "$namespace" get pod \
  -l "app.kubernetes.io/instance=$release,app.kubernetes.io/component=scan-worker" \
  -o jsonpath='{.items[0].metadata.name}')"
kubectl --kubeconfig "$kubeconfig" --context "$context" --namespace "$namespace" \
  exec "$worker_pod" -- sh -ec '
    mkdir -p /workspace/fixture
    cd /workspace/fixture
    git init --initial-branch=main --quiet
    git config user.email qualification@wolf.local
    git config user.name "Wolf Qualification"
    printf "%s\n" "import subprocess" \
      "subprocess.call(\"printf kind-qualification\", shell=True)" >main.py
    git add main.py
    git commit --quiet -m fixture
  '

forward_port="$(python3 - <<'PY'
import socket
with socket.socket() as sock:
    sock.bind(("127.0.0.1", 0))
    print(sock.getsockname()[1])
PY
)"
start_port_forward() {
  if [[ -n "$forward_pid" ]]; then
    kill -TERM "$forward_pid" >/dev/null 2>&1 || true
    wait "$forward_pid" >/dev/null 2>&1 || true
  fi
  kubectl --kubeconfig "$kubeconfig" --context "$context" --namespace "$namespace" \
    port-forward "service/${release}-wolf" "$forward_port:8778" \
    >"$work/port-forward.log" 2>&1 &
  forward_pid="$!"
}
start_port_forward

run_probe() {
  sequence="$1"
  env \
    WOLF_E2E_URL="http://127.0.0.1:$forward_port/api/v1" \
    WOLF_E2E_SOURCE_PATH=/workspace/fixture \
    WOLF_E2E_TIMEOUT_SECONDS="$timeout_seconds" \
    WOLF_E2E_EXPECTED_BACKEND=kubernetes \
    WOLF_E2E_EXPECTED_SCANNER_DIGEST="${scanner_image##*@}" \
    WOLF_E2E_EVIDENCE_PATH="$work/run-$sequence.json" \
    "$probe"
}

if ! run_probe first; then
  kubectl --kubeconfig "$kubeconfig" --context "$context" --namespace "$namespace" \
    get pods -o wide >&2 || true
  while IFS= read -r pod; do
    echo "qualification logs: $pod" >&2
    kubectl --kubeconfig "$kubeconfig" --context "$context" --namespace "$namespace" \
      logs "$pod" --all-containers --tail=300 >&2 || true
  done < <(kubectl --kubeconfig "$kubeconfig" --context "$context" \
    --namespace "$namespace" get pods -o name)
  kubectl --kubeconfig "$kubeconfig" --context "$context" --namespace "$namespace" \
    get events --sort-by=.lastTimestamp >&2 || true
  exit 1
fi

kubectl --kubeconfig "$kubeconfig" --context "$context" --namespace "$namespace" \
  rollout restart "deployment/$api_deployment" "deployment/$worker_deployment"
kubectl --kubeconfig "$kubeconfig" --context "$context" --namespace "$namespace" \
  rollout status "deployment/$api_deployment" --timeout=300s
kubectl --kubeconfig "$kubeconfig" --context "$context" --namespace "$namespace" \
  rollout status "deployment/$worker_deployment" --timeout=300s
start_port_forward
if ! run_probe after-restart; then
  kubectl --kubeconfig "$kubeconfig" --context "$context" --namespace "$namespace" \
    logs "deployment/$worker_deployment" --tail=300 >&2 || true
  exit 1
fi

jq -n \
  --arg runtimeImage "$runtime_image" \
  --arg scannerImage "$scanner_image" \
  --arg postgresImage "$postgres_image" \
  --slurpfile first "$work/run-first.json" \
  --slurpfile restarted "$work/run-after-restart.json" \
  '{
    schemaVersion:"wolf.remote-scan-kind-qualification/v1",
    database:"postgres",
    runtimeImage:$runtimeImage,
    scannerImage:$scannerImage,
    postgresImage:$postgresImage,
    initial:$first[0],
    afterRestart:$restarted[0],
    restartRecoveryVerified:true,
    result:"passed"
  }'
