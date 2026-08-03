#!/bin/sh
set -eu

if [ "${WOLF_RUN_ROLLOUT_KIND_E2E:-0}" != "1" ]; then
  echo "SKIP: set WOLF_RUN_ROLLOUT_KIND_E2E=1 to run real Kind rollout E2E"
  exit 0
fi

qualification_dir="${WOLF_RELEASE_QUALIFICATION_DIR:-/usr/local/libexec/wolf/release-qualification}"

for dependency in kind kubectl docker; do
  command -v "$dependency" >/dev/null 2>&1 || {
    echo "$dependency is required for Kind rollout E2E" >&2
    exit 2
  }
done
test -x "$qualification_dir/scanner-rollout-qualification.test" || {
  echo "trusted rollout qualification binary is unavailable in $qualification_dir" >&2
  exit 2
}

cluster="${WOLF_ROLLOUT_KIND_E2E_CLUSTER:-wolf-rollout-e2e-$$}"
context="kind-$cluster"
namespace="${WOLF_ROLLOUT_KIND_E2E_NAMESPACE:-wolf-rollout-e2e}"
ca_file="$(mktemp "${TMPDIR:-/tmp}/wolf-kind-ca.XXXXXX")"
kubeconfig="$(mktemp "${TMPDIR:-/tmp}/wolf-kind-kubeconfig.XXXXXX")"
created_cluster=0
registry_container="${WOLF_SCANNER_E2E_REGISTRY_CONTAINER:-}"
registry_endpoint="${WOLF_SCANNER_E2E_REGISTRY_ENDPOINT:-}"
registry_connected=0
: "${WOLF_KIND_API_ADDRESS:?reachable remote Kind API address is required}"
: "${WOLF_KIND_ROLLOUT_API_PORT:?remote Kind rollout API port is required}"
case "$WOLF_KIND_API_ADDRESS:$WOLF_KIND_ROLLOUT_API_PORT" in
  *[!0-9.:]*) echo "remote Kind API address/port is invalid" >&2; exit 2 ;;
esac

cleanup() {
  if [ "$registry_connected" -eq 1 ]; then
    docker network disconnect kind "$registry_container" >/dev/null 2>&1 || true
  fi
  rm -f "$ca_file" "$kubeconfig" "${kubeconfig}.kind.yaml"
  if [ "$created_cluster" -eq 1 ]; then
    kind delete cluster --name "$cluster" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

if kind get clusters | grep -Fx "$cluster" >/dev/null 2>&1; then
  echo "refusing to reuse existing Kind cluster $cluster" >&2
  exit 2
fi
cat >"${kubeconfig}.kind.yaml" <<EOF
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
networking:
  apiServerAddress: "$WOLF_KIND_API_ADDRESS"
  apiServerPort: $WOLF_KIND_ROLLOUT_API_PORT
EOF
if [ -n "$registry_container" ] || [ -n "$registry_endpoint" ]; then
  if [ -z "$registry_container" ] || ! printf '%s' "$registry_endpoint" | grep -Eq '^https?://[^[:space:]]+$'; then
    echo "local registry qualification requires both WOLF_SCANNER_E2E_REGISTRY_CONTAINER and a valid WOLF_SCANNER_E2E_REGISTRY_ENDPOINT" >&2
    exit 2
  fi
  cat >>"${kubeconfig}.kind.yaml" <<'EOF'
containerdConfigPatches:
  - |-
    [plugins."io.containerd.grpc.v1.cri".registry]
      config_path = "/etc/containerd/certs.d"
EOF
fi
kind create cluster --name "$cluster" --wait 120s --kubeconfig "$kubeconfig" \
  --config "${kubeconfig}.kind.yaml"
created_cluster=1

requested_image="${WOLF_ROLLOUT_KIND_E2E_IMAGE:-docker.io/library/alpine:3.20}"
requested_old_image="${WOLF_ROLLOUT_KIND_E2E_OLD_IMAGE:-docker.io/library/alpine:3.19}"

resolve_image() {
  requested="$1"
  case "$requested" in
    *@sha256:*)
      # The reference is already immutable. Kind performs the real pull and
      # imageID digest readback after the optional node-local mirror has been
      # configured; pulling through the Docker daemon here would bypass that
      # topology and can make an otherwise valid local-mirror drill fail.
      printf '%s\n' "$requested"
      ;;
    *)
      docker pull "$requested" >/dev/null
      docker image inspect --format '{{index .RepoDigests 0}}' "$requested"
      ;;
  esac
}

exact_image="$(resolve_image "$requested_image")"
exact_old_image="$(resolve_image "$requested_old_image")"
if [ "$exact_image" = "$exact_old_image" ]; then
  echo "Kind rollout qualification requires distinct candidate and stable exact images" >&2
  exit 2
fi

if [ -n "$registry_container" ] || [ -n "$registry_endpoint" ]; then
  registry_host="${exact_image%%/*}"
  old_registry_host="${exact_old_image%%/*}"
  if [ "$registry_host" = "$exact_image" ] || [ "$old_registry_host" != "$registry_host" ]; then
    echo "local registry qualification requires candidate and stable images from one explicit registry host" >&2
    exit 2
  fi
  docker inspect "$registry_container" >/dev/null
  docker network connect kind "$registry_container"
  registry_connected=1
  node="${cluster}-control-plane"
  docker exec "$node" mkdir -p "/etc/containerd/certs.d/${registry_host}"
  printf '[host."%s"]\n  capabilities = ["pull", "resolve"]\n' "$registry_endpoint" \
    | docker exec -i "$node" sh -c "cat >'/etc/containerd/certs.d/${registry_host}/hosts.toml'"
fi

kubectl --kubeconfig "$kubeconfig" --context "$context" create namespace "$namespace"
kubectl --kubeconfig "$kubeconfig" --context "$context" --namespace "$namespace" apply -f - <<'EOF'
apiVersion: v1
kind: ServiceAccount
metadata:
  name: wolf-rollout-e2e
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: wolf-rollout-e2e
rules:
  - apiGroups: [""]
    resources: ["configmaps", "pods", "pods/log"]
    verbs: ["get", "list", "create", "patch", "delete"]
  - apiGroups: ["apps"]
    resources: ["deployments"]
    verbs: ["get", "list", "patch"]
  - apiGroups: ["batch"]
    resources: ["jobs"]
    verbs: ["get", "list", "create", "delete"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: wolf-rollout-e2e
subjects:
  - kind: ServiceAccount
    name: wolf-rollout-e2e
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: wolf-rollout-e2e
EOF

kubectl --kubeconfig "$kubeconfig" --context "$context" --namespace "$namespace" apply -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: scanner-canary
  labels:
    wolf.dev/scanner-cohort: canary
spec:
  replicas: 1
  selector:
    matchLabels:
      app: scanner-canary
  template:
    metadata:
      labels:
        app: scanner-canary
    spec:
      containers:
        - name: scanner
          image: "$exact_image"
          command: ["/bin/sh", "-c", "trap : TERM INT; sleep 3600 & wait"]
EOF
kubectl --kubeconfig "$kubeconfig" --context "$context" --namespace "$namespace" \
  rollout status deployment/scanner-canary --timeout=120s

api_server="$(
  kubectl --kubeconfig "$kubeconfig" config view --context "$context" --minify --raw \
    -o jsonpath='{.clusters[0].cluster.server}'
)"
ca_data="$(
  kubectl --kubeconfig "$kubeconfig" config view --context "$context" --minify --raw \
    -o jsonpath='{.clusters[0].cluster.certificate-authority-data}'
)"
if printf '%s' "$ca_data" | base64 -d >"$ca_file" 2>/dev/null; then
  :
else
  printf '%s' "$ca_data" | base64 -D >"$ca_file"
fi
token="$(
  kubectl --kubeconfig "$kubeconfig" --context "$context" --namespace "$namespace" \
    create token wolf-rollout-e2e --duration=30m
)"

export WOLF_ROLLOUT_KIND_E2E_API="$api_server"
export WOLF_ROLLOUT_KIND_E2E_NAMESPACE="$namespace"
export WOLF_ROLLOUT_KIND_E2E_TOKEN="$token"
export WOLF_ROLLOUT_KIND_E2E_CA_FILE="$ca_file"
export WOLF_ROLLOUT_E2E_NEW_IMAGE="$exact_image"
export WOLF_ROLLOUT_E2E_OLD_IMAGE="$exact_old_image"

"$qualification_dir/scanner-rollout-qualification.test" \
  -test.run '^TestRealKindCohortJobAndRollback$' \
  -test.count=1 -test.v
