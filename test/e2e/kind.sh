#!/usr/bin/env bash
# Kind acceptance loop: taint → eviction deny → scale 3→4 → SpareReady →
# eviction allow (one at-risk at a time) → scale-back → Cooling → close.
# Uses the real pods/eviction API (not kubectl delete, which bypasses the webhook).
# Requires docker, kind, kubectl, helm, and Go 1.27+ only if the image is not prebuilt.
#
# Options (flags or env):
#   --verbose / -v / VERBOSE=1  dump Policy, Window, Deploy, nodes, webhook, manager logs
#   --keep     / KEEP=1         leave the kind cluster up after the run
# After a KEEP run: kind delete cluster --name "${CLUSTER:-eviction-guard}"
#
# Examples:
#   make test-kind
#   make test-kind ARGS='--verbose'
#   VERBOSE=1 KEEP=1 ./test/e2e/kind.sh
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
CLUSTER="${CLUSTER:-eviction-guard}"
IMG="${IMG:-eviction-guard:e2e}"
KEEP="${KEEP:-0}"
VERBOSE="${VERBOSE:-0}"
# Bump when e2e diagnostics change — printed at start so you can confirm which script ran.
SCRIPT_REV="2026-09-07-v4"

usage() {
  cat <<'EOF'
Kind e2e: taint → eviction deny → scale → SpareReady → eviction allow → scale-back.

Options (flags or env):
  --verbose / -v / VERBOSE=1  dump Policy, Window, Deploy, nodes, webhook, manager logs
  --keep     / KEEP=1         leave the kind cluster up after the run
  -h / --help                 this message

Examples:
  make test-kind
  make test-kind ARGS='--verbose'
  VERBOSE=1 KEEP=1 ./test/e2e/kind.sh
EOF
  exit "${1:-0}"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    -v|--verbose) VERBOSE=1 ;;
    --keep) KEEP=1 ;;
    -h|--help) usage 0 ;;
    *)
      echo "unknown option: $1" >&2
      usage 1
      ;;
  esac
  shift
done

need() {
  command -v "$1" >/dev/null || { echo "need $1 on PATH" >&2; exit 1; }
}
need docker
need kind
need kubectl
need helm

# Fail early with an actionable message (kind's docker error is easy to miss).
if ! docker info >/dev/null 2>&1; then
  echo "docker is installed but not usable (cannot talk to the daemon)." >&2
  echo "  socket error is usually: permission denied on /var/run/docker.sock" >&2
  echo "Fix (pick one):" >&2
  echo "  1) sudo usermod -aG docker \"\$USER\" && newgrp docker   # then re-login" >&2
  echo "  2) rootless: dockerd-rootless-setuptool.sh install" >&2
  echo "  3) confirm the daemon is running: sudo systemctl status docker" >&2
  docker info >/dev/null || true
  exit 1
fi

echo "==> kind e2e script rev=${SCRIPT_REV}  verbose=${VERBOSE}  keep=${KEEP}  cluster=${CLUSTER}"
if [[ "$VERBOSE" == "1" ]]; then
  echo "    verbose mode: plain-English state summary + Policy/Window/Deploy YAML at each explore step"
fi

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
  explore "timeout:$desc"
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

spare_ready() {
  local got
  got="$(kubectl get egw -n default -o jsonpath='{.items[0].status.spareReady}' 2>/dev/null || true)"
  [[ "$got" == "true" ]]
}

no_windows() {
  local n
  n="$(kubectl get egw -n default --no-headers 2>/dev/null | wc -l | tr -d ' ')"
  [[ "$n" == "0" ]]
}

eviction_webhook_registered() {
  kubectl get validatingwebhookconfiguration -o name 2>/dev/null | grep -q eviction-guard
  kubectl get validatingwebhookconfiguration -o yaml 2>/dev/null | grep -q 'pods/eviction'
}

# Must stay in bash: Ubuntu's /bin/sh is dash and does not implement [[.
webhook_ready() {
  local ip
  ip="$(kubectl get endpoints -n eviction-guard-system eviction-guard-webhook -o jsonpath='{.subsets[0].addresses[0].ip}' 2>/dev/null || true)"
  if [[ -n "$ip" ]]; then
    return 0
  fi
  ip="$(kubectl get endpointslice -n eviction-guard-system -l kubernetes.io/service-name=eviction-guard-webhook -o jsonpath='{.items[0].endpoints[0].addresses[0]}' 2>/dev/null || true)"
  [[ -n "$ip" ]]
}

# POST policy/v1 Eviction for a pod. Prints API stderr on failure.
# Returns kubectl's exit code (0 = admitted and eviction created).
evict_pod() {
  local pod="$1"
  kubectl create --raw "/api/v1/namespaces/default/pods/${pod}/eviction" -f - <<EOF
{
  "apiVersion": "policy/v1",
  "kind": "Eviction",
  "metadata": {
    "name": "${pod}",
    "namespace": "default"
  }
}
EOF
}

# Lexicographically first at-risk web pod on NODE (same rule as pkg/evictgate).
# Use go-template (not jsonpath deletionTimestamp==null — that filter often matches nothing).
at_risk_pods() {
  local node="$1"
  kubectl get po -n default -l app=web \
    --field-selector "spec.nodeName=${node}" \
    -o go-template='{{range .items}}{{if and (not .metadata.deletionTimestamp) (eq (index .metadata.labels "eviction-guard.io/protected") "true")}}{{.metadata.name}}{{"\n"}}{{end}}{{end}}' \
    2>/dev/null | sort || true
}

next_at_risk_pod() {
  local node="$1"
  # Avoid `... | head` under pipefail (SIGPIPE aborts the script).
  local pods
  pods="$(at_risk_pods "$node")"
  [[ -n "$pods" ]] || return 0
  printf '%s\n' "$pods" | sed -n '1p'
}

expect_eviction_denied() {
  local pod="$1"
  local want_substr="$2"
  local out rc
  set +e
  out="$(evict_pod "$pod" 2>&1)"
  rc=$?
  set -e
  if [[ "$rc" -eq 0 ]]; then
    echo "expected eviction of $pod to be denied, but it was allowed" >&2
    echo "$out" >&2
    return 1
  fi
  if ! grep -q "$want_substr" <<<"$out"; then
    echo "eviction deny for $pod missing $want_substr: $out" >&2
    return 1
  fi
  echo "ok  eviction denied for $pod ($want_substr)"
  if [[ "$VERBOSE" == "1" ]]; then
    echo "    deny message: $(echo "$out" | tr '\n' ' ' | sed 's/  */ /g')"
    explore "after eviction deny ($pod)"
  fi
}

expect_eviction_allowed() {
  local pod="$1"
  local out rc
  set +e
  out="$(evict_pod "$pod" 2>&1)"
  rc=$?
  set -e
  if [[ "$rc" -ne 0 ]]; then
    echo "expected eviction of $pod to be allowed: $out" >&2
    return 1
  fi
  wait_ok "pod $pod gone after eviction" 60 pod_gone "$pod"
  echo "ok  eviction allowed for $pod"
  if [[ "$VERBOSE" == "1" ]]; then
    explore "after eviction allow ($pod)"
  fi
}

pod_gone() {
  local pod="$1"
  ! kubectl get po -n default "$pod" >/dev/null 2>&1
}

show_placement() {
  local label="$1"
  echo "    [$label] pod placement:"
  for node in control-plane worker worker2; do
    local node_name
    node_name="$(kubectl get nodes -l "kubernetes.io/hostname=${CLUSTER}-${node}" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)"
    [[ -z "$node_name" ]] && continue
    local count
    count="$(kubectl get po -n default -l app=web --field-selector "spec.nodeName=$node_name" --no-headers 2>/dev/null | wc -l | tr -d ' ')"
    printf '        %-28s pods: %s\n' "$node_name" "$count"
    kubectl get po -n default -l app=web --field-selector "spec.nodeName=$node_name" \
      -o custom-columns='NAME:.metadata.name,READY:.status.conditions[?(@.type=="Ready")].status,PROTECTED:.metadata.labels.eviction-guard\.io/protected' \
      --no-headers 2>/dev/null | sed 's/^/          /' || true
  done
}

# Plain-English snapshot of the moving parts (Policy / Window / Deploy / pods / nodes).
summarize_moving_parts() {
  local label="$1"
  echo
  echo "======== MOVING PARTS [$label] ========"
  echo "-- story --"
  case "$label" in
    baseline)
      echo "Setup only: Policy watches Spot nodes; workload is opted-in; no disruption yet."
      echo "Expect: egp Ready, matchedNodes>0, NO egw, deploy replicas=3."
      ;;
    "right after taint"*)
      echo "Node just tainted with karpenter.sh/disrupted. Controller should open an egw and scale."
      echo "Expect soon: egw phase=Open, deploy replicas>3, SpareReady may still be false."
      ;;
    "after eviction deny"*)
      echo "Voluntary Eviction was DENIED by the pods/eviction webhook (spare not ready / wrong pod)."
      echo "Expect: pod still Running; egw may be Open with spareReady=false OR spareReady=true gating order."
      ;;
    "after eviction allow"*)
      echo "Voluntary Eviction was ALLOWED for the next at-risk pod (SpareReady + lex-first)."
      echo "Expect: that pod terminating/gone; other at-risk pods still denied until they are next."
      ;;
    "scaled up + spare Ready")
      echo "Spare capacity is Ready off the dying node — drain may proceed one pod at a time."
      echo "Expect: egw spareReady=true, scaledTo>baseline, extra pods on a healthy node."
      ;;
    cooling*)
      echo "At-risk pods gone; capacity restored; Cooling until scaleBackAfter elapses."
      echo "Expect: deploy replicas back to baseline, egw phase=Cooling, node may still be tainted."
      ;;
    final)
      echo "Done. Window deleted after cooldown; cluster back to steady state."
      echo "Expect: no egw, deploy replicas=3."
      ;;
    timeout:*)
      echo "Timed out waiting for a condition — dump below is for debugging."
      ;;
    *)
      echo "Checkpoint: inspect Policy + Window + Deploy + placement."
      ;;
  esac

  echo
  echo "-- EvictionGuardPolicy --"
  if ! kubectl get egp >/dev/null 2>&1; then
    echo "(none)"
  else
    kubectl get egp -o custom-columns='NAME:.metadata.name,MATCHED:.status.matchedNodes,VULN:.status.vulnerableNodes,WINDOWS:.status.activeWindows,DEFERRED:.status.deferredWorkloads,READY:.status.conditions[?(@.type=="Ready")].status,RBAC:.status.conditions[?(@.type=="BackendsAuthorized")].status' 2>/dev/null || kubectl get egp -o wide
    echo "spec.backends keys: $(kubectl get egp -o jsonpath='{range .items[*].spec.backends}{@range $k, $v := .}}{{$k}} {{end}}{{end}}' 2>/dev/null)"
    echo "disruptionSignals: $(kubectl get egp -o jsonpath='{.items[0].spec.disruptionSignals}' 2>/dev/null)"
    echo "nodeFilter.capacityTypes: $(kubectl get egp -o jsonpath='{.items[0].spec.nodeFilter.capacityTypes}' 2>/dev/null)"
  fi

  echo
  echo "-- EvictionGuardWindow --"
  local egw_n
  egw_n="$(kubectl get egw -A --no-headers 2>/dev/null | wc -l | tr -d ' ')"
  if [[ "${egw_n:-0}" == "0" ]]; then
    echo "(none — no open disruption window)"
  else
    kubectl get egw -A -o custom-columns='NS:.metadata.namespace,NAME:.metadata.name,POLICY:.spec.policyName,PHASE:.status.phase,SPAREREADY:.status.spareReady,BASE:.spec.baseline,TO:.spec.scaledTo,SAFE:.status.safeReadyReplicas,READY:.status.readyReplicas,MSG:.status.message' 2>/dev/null || kubectl get egw -A -o wide
    echo "vulnerableNodes: $(kubectl get egw -A -o jsonpath='{.items[0].spec.vulnerableNodes}' 2>/dev/null)"
    echo "actions:"
    kubectl get egw -A -o jsonpath='{range .items[0].spec.actions[*]}  key={.key} kind={.kind} name={.name} baseline={.baseline} scaledTo={.scaledTo}{"\n"}{end}' 2>/dev/null || true
  fi

  echo
  echo "-- Deployment web --"
  kubectl get deploy web -n default -o custom-columns='NAME:.metadata.name,DESIRED:.spec.replicas,READY:.status.readyReplicas,UPTODATE:.status.updatedReplicas,AVAILABLE:.status.availableReplicas' 2>/dev/null || echo "(no deploy/web)"
  echo "annotations: $(kubectl get deploy web -n default -o jsonpath='{.metadata.annotations}' 2>/dev/null)"

  echo
  echo "-- protected web pods --"
  kubectl get po -n default -l app=web -o custom-columns='NAME:.metadata.name,NODE:.spec.nodeName,PHASE:.status.phase,READY:.status.containerStatuses[0].ready,PROTECTED:.metadata.labels.eviction-guard\.io/protected,DELETING:.metadata.deletionTimestamp' 2>/dev/null || true

  echo
  echo "-- nodes --"
  kubectl get nodes -o custom-columns='NAME:.metadata.name,CAPACITY:.metadata.labels.karpenter\.sh/capacity-type,IGNORE:.metadata.labels.eviction-guard\.io/ignore,CORDON:.spec.unschedulable,TAINTS:.spec.taints[*].key' 2>/dev/null || true
  echo "======== end moving parts [$label] ========"
  echo
}

# Dump the objects operators care about at this stage (follow-along / debug).
explore() {
  local label="$1"
  echo
  echo "---- explore [$label] ----"
  kubectl get egp -o wide 2>/dev/null || true
  kubectl get egw -A -o wide 2>/dev/null || true
  kubectl get deploy,po -n default -l app=web -o wide 2>/dev/null || true
  kubectl get events -n default --field-selector involvedObject.kind=EvictionGuardWindow --sort-by=.lastTimestamp 2>/dev/null | tail -n 8 || true
  show_placement "$label"

  if [[ "$VERBOSE" == "1" ]]; then
    summarize_moving_parts "$label"
    echo "---- verbose YAML: EvictionGuardPolicy ----"
    kubectl get egp -o yaml 2>/dev/null || true
    echo "---- verbose YAML: EvictionGuardWindow ----"
    if [[ "$(kubectl get egw -A --no-headers 2>/dev/null | wc -l | tr -d ' ')" == "0" ]]; then
      echo "(none)"
    else
      kubectl get egw -A -o yaml 2>/dev/null || true
    fi
    echo "---- verbose YAML: Deployment/web ----"
    kubectl get deploy web -n default -o yaml 2>/dev/null || true
    if [[ "$label" != "baseline" ]]; then
      echo "---- verbose: manager logs (last 60 lines) ----"
      kubectl logs -n eviction-guard-system -l control-plane=controller-manager --tail=60 2>/dev/null || true
      echo "---- verbose: recent Events (default ns) ----"
      kubectl get events -n default --sort-by=.lastTimestamp 2>/dev/null | tail -n 20 || true
    else
      echo "---- verbose: manager logs ----"
      echo "(skipped at baseline — boot-only Starting EventSource lines; shown after disruption)"
    fi
  fi

  echo "---- end explore [$label] ----"
  echo
}

cleanup() {
  if [[ "$KEEP" == "1" ]]; then
    echo
    echo "KEEP=1: leaving cluster $CLUSTER for exploration."
    echo "  kubectl get egp,egw,deploy,po -A"
    echo "  kubectl describe egw -n default"
    echo "  kubectl get po -n default -l app=web --show-labels"
    echo "Cleanup when done:"
    echo "  kind delete cluster --name $CLUSTER"
    return
  fi
  echo "==> cleanup: deleting kind cluster $CLUSTER"
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
# Disable BuildKit attestations — kind load uses `ctr import --all-platforms` and
# fails (EOF / missing digest) when the image is an OCI index with attestation manifests.
docker build --provenance=false --sbom=false -t "$IMG" "$ROOT"

load_image_into_kind() {
  local img="$1" cluster="$2"
  if kind load docker-image "$img" --name "$cluster"; then
    return 0
  fi
  echo "    kind load docker-image failed; retrying via docker save + ctr (no --all-platforms)" >&2
  local node
  for node in $(kind get nodes --name "$cluster"); do
    echo "    loading $img -> $node"
    docker save "$img" | docker exec -i "$node" \
      ctr --namespace=k8s.io images import --digests - >/dev/null
  done
}

load_image_into_kind "$IMG" "$CLUSTER"

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
wait_ok "webhook endpoints" 60 webhook_ready
wait_ok "eviction webhook registered" 30 eviction_webhook_registered

echo "==> webhook rejects bad scale-backend syntax"
set +e
deny_out="$(kubectl apply -f - <<'EOF' 2>&1
apiVersion: apps/v1
kind: Deployment
metadata:
  name: web-bad
  annotations:
    eviction-guard.io/scale-backend: "=missing-key"
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
echo "$deny_out" | grep -qi "empty scale-backend key\|scale-backend" || {
  echo "unexpected deny: $deny_out" >&2
  exit 1
}
echo "ok  webhook denied invalid scale-backend"

kubectl apply -f "$ROOT/test/e2e/policy.yaml"
kubectl apply -f "$ROOT/test/e2e/workload.yaml"
wait_ok "web has 3 Ready pods" 180 \
  kubectl wait --for=condition=available deploy/web -n default --timeout=150s
explore "baseline"

echo "==> next step: taint a worker node that hosts a protected web pod"
# Do not pipe to `head` under pipefail (SIGPIPE → exit 141).
NODE="$(kubectl get po -n default -l app=web \
  -o go-template='{{range .items}}{{if and .spec.nodeName (eq (index .metadata.labels "eviction-guard.io/protected") "true")}}{{.spec.nodeName}}{{"\n"}}{{end}}{{end}}')"
NODE="${NODE%%$'\n'*}"
NODE="${NODE//$'\r'/}"
if [[ -z "$NODE" ]]; then
  echo "could not find a scheduled protected web pod" >&2
  kubectl get po -n default -l app=web -o wide --show-labels >&2 || true
  exit 1
fi
echo "    disrupting node: $NODE"
AT_RISK="$(next_at_risk_pod "$NODE")"
if [[ -z "$AT_RISK" ]]; then
  echo "no at-risk pod on $NODE (protected+Running)" >&2
  echo "pods on that node:" >&2
  kubectl get po -n default -l app=web --field-selector "spec.nodeName=${NODE}" -o wide --show-labels >&2 || true
  exit 1
fi
echo "    first at-risk pod: $AT_RISK"
kubectl taint node "$NODE" karpenter.sh/disrupted=:NoSchedule --overwrite
if [[ "$VERBOSE" == "1" ]]; then
  explore "right after taint (window should open next)"
fi

echo "==> pods/eviction denied before SpareReady"
expect_eviction_denied "$AT_RISK" "waiting for eviction-guard"

wait_ok "scaled above 3 (atRisk may be 1 or 2)" 90 scaled_up
wait_ok "window Open" 60 window_phase Open
# SpareReady waits on readinessProbe delay for the new replica.
wait_ok "spare Ready" 180 spare_ready
explore "scaled up + spare Ready"

echo "==> pods/eviction allow/deny after SpareReady (one at-risk at a time)"
mapfile -t RISK_PODS < <(at_risk_pods "$NODE")
if ((${#RISK_PODS[@]} == 0)); then
  echo "no at-risk pods left on $NODE after SpareReady" >&2
  exit 1
fi
NEXT="${RISK_PODS[0]}"
if ((${#RISK_PODS[@]} > 1)); then
  OTHER="${RISK_PODS[$((${#RISK_PODS[@]} - 1))]}"
  if [[ "$OTHER" != "$NEXT" ]]; then
    expect_eviction_denied "$OTHER" "waiting for eviction of ${NEXT} first"
  fi
fi

while true; do
  NEXT="$(next_at_risk_pod "$NODE")"
  [[ -n "$NEXT" ]] || break
  expect_eviction_allowed "$NEXT"
done
echo "ok  all at-risk pods on $NODE evicted via pods/eviction"

# Scale-back must not wait for the node taint to clear.
wait_ok "scaled back to 3 (at-risk gone)" 90 replicas_are 3
wait_ok "window Cooling" 60 window_phase Cooling
explore "cooling (scaled back; node may still be tainted)"

echo "==> clear disruption taint (optional for close; proves we did not wait on it)"
kubectl taint node "$NODE" karpenter.sh/disrupted:NoSchedule- || true
wait_ok "window closed" 60 no_windows
explore "final"

echo
echo "kind e2e passed: taint → eviction deny → scale → SpareReady → eviction allow → scale-back → Cooling → close"
