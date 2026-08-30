#!/usr/bin/env bash
# Kind acceptance loop: taint → scale 3→4 → drain → SpareReady → cooldown → 4→3.
# Requires docker, kind, kubectl, helm, and Go 1.27+ only if the image is not prebuilt.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
CLUSTER="${CLUSTER:-eviction-guard}"
IMG="${IMG:-eviction-guard:e2e}"
KEEP="${KEEP:-0}"

need() {
  command -v "$1" >/dev/null || { echo "need $1 on PATH" >&2; exit 1; }
}
need docker
need kind
need kubectl
need helm

wait_ok() {
  local desc="$1" timeout="$2"
  shift 2
  local end=$((SECONDS + timeout))
  while ((SECONDS < end)); do
    if "$@" >/dev/null 2>&1; then
      echo "ok  $desc"
      return 0
    fi
    sleep 2
  done
  echo "timeout waiting for: $desc" >&2
  kubectl get deploy,po,egp,egw -A || true
  return 1
}

replicas_are() {
  local want="$1"
  local got
  got="$(kubectl get deploy web -n default -o jsonpath='{.spec.replicas}')"
  [[ "$got" == "$want" ]]
}

scaled_up() {
  local got
  got="$(kubectl get deploy web -n default -o jsonpath='{.spec.replicas}')"
  [[ "${got:-0}" -gt 3 ]]
}

window_phase() {
  local want="$1"
  local got
  got="$(kubectl get egw -n default -o jsonpath='{.items[0].status.phase}' 2>/dev/null || true)"
  [[ "$got" == "$want" ]]
}

no_windows() {
  local n
  n="$(kubectl get egw -n default --no-headers 2>/dev/null | wc -l | tr -d ' ')"
  [[ "$n" == "0" ]]
}

cleanup() {
  if [[ "$KEEP" == "1" ]]; then
    echo "KEEP=1: leaving cluster $CLUSTER"
    return
  fi
  kind delete cluster --name "$CLUSTER" >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "==> kind cluster $CLUSTER (1 control-plane + 2 spot workers)"
if kind get clusters 2>/dev/null | grep -qx "$CLUSTER"; then
  kind delete cluster --name "$CLUSTER"
fi
kind create cluster --name "$CLUSTER" --config "$ROOT/test/e2e/kind-config.yaml"

kubectl label node "${CLUSTER}-control-plane" eviction-guard.io/ignore=true --overwrite
for n in $(kubectl get nodes -l '!node-role.kubernetes.io/control-plane' -o jsonpath='{.items[*].metadata.name}'); do
  kubectl label node "$n" karpenter.sh/capacity-type=spot --overwrite
done

echo "==> build and load $IMG"
docker build -t "$IMG" "$ROOT"
kind load docker-image "$IMG" --name "$CLUSTER"

echo "==> install eviction-guard"
helm upgrade --install eviction-guard "$ROOT/charts/eviction-guard" \
  --namespace eviction-guard-system --create-namespace \
  --set image.repository="${IMG%%:*}" \
  --set image.tag="${IMG##*:}" \
  --set image.pullPolicy=IfNotPresent \
  --set leaderElect=false \
  --wait --timeout 3m

wait_ok "manager ready" 120 \
  kubectl wait --for=condition=available deploy -n eviction-guard-system -l control-plane=controller-manager --timeout=90s
wait_ok "webhook endpoints" 60 \
  sh -c 'ip=$(kubectl get endpoints -n eviction-guard-system eviction-guard-webhook -o jsonpath="{.subsets[0].addresses[0].ip}" 2>/dev/null); [[ -n "$ip" ]]'

echo "==> webhook rejects bad scale-backend"
set +e
deny_out="$(kubectl apply -f - <<'EOF' 2>&1
apiVersion: apps/v1
kind: Deployment
metadata:
  name: web-bad
  labels:
    eviction-guard.io/enabled: "true"
  annotations:
    eviction-guard.io/scale-backend: nope
spec:
  replicas: 1
  selector:
    matchLabels:
      app: web-bad
  template:
    metadata:
      labels:
        app: web-bad
    spec:
      containers:
        - name: pause
          image: registry.k8s.io/pause:3.9
EOF
)"
deny_rc=$?
set -e
if [[ "$deny_rc" -eq 0 ]]; then
  echo "expected webhook to deny web-bad" >&2
  exit 1
fi
echo "$deny_out" | grep -q "unknown scale backend" || { echo "unexpected deny: $deny_out" >&2; exit 1; }
echo "ok  webhook denied invalid scale-backend"

kubectl apply -f "$ROOT/test/e2e/policy.yaml"
kubectl apply -f "$ROOT/examples/workload.yaml"
wait_ok "web has 3 Ready pods" 180 \
  kubectl wait --for=condition=available deploy/web -n default --timeout=150s

echo "==> taint a node that is running web"
NODE="$(kubectl get po -n default -l app=web -o jsonpath='{.items[0].spec.nodeName}')"
echo "    draining via taint on $NODE"
kubectl taint node "$NODE" karpenter.sh/disrupted=:NoSchedule --overwrite

wait_ok "scaled above 3 (atRisk may be 1 or 2)" 90 scaled_up
wait_ok "window Open" 60 window_phase Open

echo "==> simulate Karpenter eviction (delete pods still on $NODE)"
kubectl delete po -n default -l app=web --field-selector "spec.nodeName=$NODE" --wait=true
wait_ok "window Cooling (SpareReady + nodes clear)" 90 window_phase Cooling
wait_ok "scaled back to 3" 90 replicas_are 3
wait_ok "window closed" 60 no_windows

echo
echo "kind e2e passed: taint → 3→4 → drain → SpareReady → cooldown → 4→3"
