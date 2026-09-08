#!/usr/bin/env bash
# Kind e2e for capacity backends:
#   1) Deployment replicas
#   2) HPA minReplicas + maxReplicas (tight min==max)
#   3) KEDA ScaledObject minReplicaCount (Recipe A)
#
# Usage:
#   make test-kind-backends                    # CI-style: create fresh, delete after
#   REUSE=1 KEEP=1 ./test/e2e/backends.sh      # local polish: reuse cluster, keep it
#   VERBOSE=1 CASES=keda-external ./test/e2e/backends.sh
#   SKIP_BUILD=1 REUSE=1 ./test/e2e/backends.sh
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
CLUSTER="${CLUSTER:-eviction-guard-backends}"
IMG="${IMG:-eviction-guard:e2e}"
# Default: fresh cluster each run + cleanup (CI). Local polish: REUSE=1 KEEP=1.
REUSE="${REUSE:-0}"
KEEP="${KEEP:-0}"
VERBOSE="${VERBOSE:-0}"
SKIP_BUILD="${SKIP_BUILD:-0}"
CASES="${CASES:-deployment,hpa,keda}"
KEDA_CHART_VERSION="${KEDA_CHART_VERSION:-2.17.2}"
SCRIPT_REV="2026-09-08-backends-v6"

usage() {
  cat <<'EOF'
Kind e2e: Deployment, HPA min+max, KEDA Recipe A, and KEDA Recipe B (external).

Options (flags or env):
  --verbose / -v / VERBOSE=1
  --keep / KEEP=1              leave cluster up after the run
  --reuse / REUSE=1            reuse existing kind cluster (implies KEEP=1)
  --fresh / REUSE=0            delete+recreate cluster (default)
  --skip-build / SKIP_BUILD=1  do not docker build (still helm upgrade + load if needed)
  --cases LIST / CASES=deployment,hpa,keda,keda-external
  -h / --help
EOF
  exit "${1:-0}"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    -v|--verbose) VERBOSE=1 ;;
    --keep) KEEP=1 ;;
    --reuse) REUSE=1; KEEP=1 ;;
    --fresh) REUSE=0 ;;
    --skip-build) SKIP_BUILD=1 ;;
    --cases)
      shift
      CASES="${1:-}"
      [[ -n "$CASES" ]] || usage 1
      ;;
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

if ! docker info >/dev/null 2>&1; then
  echo "docker is installed but not usable (cannot talk to the daemon)." >&2
  echo "  try: sudo systemctl start docker" >&2
  echo "  and: sudo usermod -aG docker \"\$USER\" && newgrp docker" >&2
  exit 1
fi

echo "==> backends e2e rev=${SCRIPT_REV} cases=${CASES} cluster=${CLUSTER} reuse=${REUSE} keep=${KEEP} skip_build=${SKIP_BUILD} verbose=${VERBOSE}"

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
  dump_state "timeout:$desc"
  return 1
}

show_placement() {
  local label="$1"
  echo "    [$label] pod placement:"
  kubectl get po -n default -l app=web -o wide --show-labels 2>/dev/null | sed 's/^/      /' || true
}

# Snapshot Policy / Window / Deploy / HPA / ScaledObject / pods / nodes.
explore() {
  local label="$1"
  echo
  echo "======== RESOURCES [$label] ========"

  echo "-- EvictionGuardPolicy --"
  kubectl get egp -o wide 2>/dev/null || echo "(none)"
  echo "backends: $(kubectl get egp -o jsonpath='{range .items[*].spec.backends}{@range $k, $v := .}}{{$k}}={{.kind}} {{end}}{{end}}' 2>/dev/null)"

  echo
  echo "-- EvictionGuardWindow --"
  if [[ "$(kubectl get egw -n default --no-headers 2>/dev/null | wc -l | tr -d ' ')" == "0" ]]; then
    echo "(none)"
  else
    kubectl get egw -n default -o custom-columns=\
'NAME:.metadata.name,PHASE:.status.phase,SPAREREADY:.status.spareReady,BASE:.spec.baseline,TO:.spec.scaledTo,SAFE:.status.safeReadyReplicas,READY:.status.readyReplicas,MSG:.status.message' \
      2>/dev/null || kubectl get egw -n default -o wide
    echo "vulnerableNodes: $(kubectl get egw -n default -o jsonpath='{.items[0].spec.vulnerableNodes}' 2>/dev/null)"
    echo "actions:"
    kubectl get egw -n default -o jsonpath='{range .items[0].spec.actions[*]}  key={.key} kind={.kind} name={.name} ns={.namespace} baseline={.baseline} scaledTo={.scaledTo}{"\n"}{end}' 2>/dev/null || true
  fi

  echo
  echo "-- Deployment web --"
  kubectl get deploy web -n default -o wide 2>/dev/null || echo "(none)"
  echo "scale-backend ann: $(kubectl get deploy web -n default -o jsonpath='{.metadata.annotations.eviction-guard\.io/scale-backend}' 2>/dev/null)"

  echo
  echo "-- HPA --"
  kubectl get hpa -n default -o wide 2>/dev/null || echo "(none)"

  echo
  echo "-- ScaledObject --"
  if kubectl get scaledobject -n default >/dev/null 2>&1; then
    kubectl get scaledobject -n default -o wide 2>/dev/null || true
    echo "minReplicaCount=$(kubectl get scaledobject web -n default -o jsonpath='{.spec.minReplicaCount}' 2>/dev/null) maxReplicaCount=$(kubectl get scaledobject web -n default -o jsonpath='{.spec.maxReplicaCount}' 2>/dev/null)"
  else
    echo "(none / CRD not present)"
  fi

  echo
  echo "-- pods (web) --"
  kubectl get po -n default -l app=web -o custom-columns=\
'NAME:.metadata.name,NODE:.spec.nodeName,PHASE:.status.phase,READY:.status.containerStatuses[0].ready,PROTECTED:.metadata.labels.eviction-guard\.io/protected,DELETING:.metadata.deletionTimestamp' \
    2>/dev/null || true
  show_placement "$label"

  echo
  echo "-- nodes --"
  kubectl get nodes -o custom-columns=\
'NAME:.metadata.name,CAPACITY:.metadata.labels.karpenter\.sh/capacity-type,IGNORE:.metadata.labels.eviction-guard\.io/ignore,TAINTS:.spec.taints[*].key' \
    2>/dev/null || true

  if [[ "$VERBOSE" == "1" ]]; then
    echo
    echo "---- YAML: EvictionGuardPolicy ----"
    kubectl get egp -o yaml 2>/dev/null || true
    echo "---- YAML: EvictionGuardWindow ----"
    kubectl get egw -n default -o yaml 2>/dev/null || echo "(none)"
    echo "---- YAML: Deployment/web ----"
    kubectl get deploy web -n default -o yaml 2>/dev/null || true
    echo "---- YAML: ScaledObject/web ----"
    kubectl get scaledobject web -n default -o yaml 2>/dev/null || echo "(none)"
    echo "---- YAML: HPA (all in default) ----"
    kubectl get hpa -n default -o yaml 2>/dev/null || echo "(none)"
    echo "---- manager logs (last 40) ----"
    kubectl logs -n eviction-guard-system -l control-plane=controller-manager --tail=40 2>/dev/null || true
    echo "---- recent Events (default) ----"
    kubectl get events -n default --sort-by=.lastTimestamp 2>/dev/null | tail -n 15 || true
  fi
  echo "======== end RESOURCES [$label] ========"
  echo
}

dump_state() {
  # Always used on failure; also used as explore alias.
  explore "$1"
}

replicas_gt() {
  local min="$1"
  local got
  got="$(kubectl get deploy web -n default -o jsonpath='{.spec.replicas}')"
  [[ "${got:-0}" -gt "$min" ]]
}

replicas_are() {
  local want="$1"
  local got
  got="$(kubectl get deploy web -n default -o jsonpath='{.spec.replicas}')"
  [[ "$got" == "$want" ]]
}

hpa_min_gt() {
  local min="$1"
  local got
  got="$(kubectl get hpa web -n default -o jsonpath='{.spec.minReplicas}')"
  [[ "${got:-0}" -gt "$min" ]]
}

hpa_max_gt() {
  local min="$1"
  local got
  got="$(kubectl get hpa web -n default -o jsonpath='{.spec.maxReplicas}')"
  [[ "${got:-0}" -gt "$min" ]]
}

so_min_gt() {
  local min="$1"
  local got
  got="$(kubectl get scaledobject web -n default -o jsonpath='{.spec.minReplicaCount}')"
  [[ "${got:-0}" -gt "$min" ]]
}

so_min_is() {
  local want="$1"
  local got
  got="$(kubectl get scaledobject web -n default -o jsonpath='{.spec.minReplicaCount}')"
  [[ "$got" == "$want" ]]
}

# Recipe B: EVG must not record capacity actions on the Window.
window_actions_empty() {
  local n
  n="$(kubectl get egw -n default -o jsonpath='{range .items[*].spec.actions[*]}x{end}' 2>/dev/null || true)"
  [[ -z "$n" ]]
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

# After scale-back, Cooling can be brief (scaleBackAfter=10s) and the window may
# already be Closed/deleted by the time we poll.
window_cooling_or_done() {
  window_phase Cooling || window_phase Closed || no_windows
}

webhook_ready() {
  local ip
  ip="$(kubectl get endpoints -n eviction-guard-system eviction-guard-webhook -o jsonpath='{.subsets[0].addresses[0].ip}' 2>/dev/null || true)"
  if [[ -n "$ip" ]]; then
    return 0
  fi
  ip="$(kubectl get endpointslice -n eviction-guard-system -l kubernetes.io/service-name=eviction-guard-webhook -o jsonpath='{.items[0].endpoints[0].addresses[0]}' 2>/dev/null || true)"
  [[ -n "$ip" ]]
}

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

at_risk_pods() {
  local node="$1"
  kubectl get po -n default -l app=web \
    --field-selector "spec.nodeName=${node}" \
    -o go-template='{{range .items}}{{if and (not .metadata.deletionTimestamp) (eq (index .metadata.labels "eviction-guard.io/protected") "true")}}{{.metadata.name}}{{"\n"}}{{end}}{{end}}' \
    2>/dev/null | sort || true
}

next_at_risk_pod() {
  local node="$1"
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
  wait_ok "pod $pod gone after eviction" 60 \
    bash -c "! kubectl get po -n default \"$pod\" >/dev/null 2>&1"
  echo "ok  eviction allowed for $pod"
}

clear_worker_taints() {
  local n
  for n in $(kubectl get nodes -l '!node-role.kubernetes.io/control-plane' -o jsonpath='{.items[*].metadata.name}'); do
    kubectl taint node "$n" karpenter.sh/disrupted:NoSchedule- >/dev/null 2>&1 || true
  done
}

cleanup_case() {
  clear_worker_taints
  kubectl delete egw --all -n default --ignore-not-found --wait=true >/dev/null 2>&1 || true
  kubectl delete egp --all --ignore-not-found --wait=true >/dev/null 2>&1 || true
  kubectl delete scaledobject --all -n default --ignore-not-found --wait=true >/dev/null 2>&1 || true
  kubectl delete hpa --all -n default --ignore-not-found --wait=true >/dev/null 2>&1 || true
  kubectl delete deploy web -n default --ignore-not-found --wait=true >/dev/null 2>&1 || true
  # Ensure no leftover pods from prior case.
  kubectl delete po -n default -l app=web --force --grace-period=0 --ignore-not-found >/dev/null 2>&1 || true
  sleep 2
}

pick_node_with_protected_pod() {
  local node
  node="$(kubectl get po -n default -l app=web \
    -o go-template='{{range .items}}{{if and .spec.nodeName (eq (index .metadata.labels "eviction-guard.io/protected") "true")}}{{.spec.nodeName}}{{"\n"}}{{end}}{{end}}')"
  node="${node%%$'\n'*}"
  node="${node//$'\r'/}"
  [[ -n "$node" ]] || return 1
  printf '%s\n' "$node"
}

# Shared disruption loop. Extra checks are case-specific callbacks.
# Callers must capture rc explicitly — do not rely on set -e across `if !`.
run_disruption_loop() {
  local case_name="$1"
  local baseline_replicas="$2"
  shift 2
  local -a post_scale_checks=("$@")

  echo
  echo "######## case: ${case_name} ########"
  wait_ok "web available (${case_name})" 180 \
    kubectl wait --for=condition=available deploy/web -n default --timeout=150s || return 1
  wait_ok "web has ${baseline_replicas} Ready replicas" 120 replicas_are "$baseline_replicas" || return 1
  if [[ "$VERBOSE" == "1" ]]; then
    explore "${case_name}:baseline"
  fi

  local NODE AT_RISK
  NODE="$(pick_node_with_protected_pod)" || {
    echo "no protected web pod scheduled" >&2
    kubectl get po -n default -l app=web -o wide --show-labels >&2 || true
    return 1
  }
  AT_RISK="$(next_at_risk_pod "$NODE")"
  [[ -n "$AT_RISK" ]] || {
    echo "no at-risk pod on $NODE" >&2
    return 1
  }
  echo "    disrupting node=$NODE at-risk=$AT_RISK"
  kubectl taint node "$NODE" karpenter.sh/disrupted=:NoSchedule --overwrite
  if [[ "$VERBOSE" == "1" ]]; then
    explore "${case_name}:right-after-taint"
  fi

  expect_eviction_denied "$AT_RISK" "waiting for eviction-guard" || return 1
  if [[ "$VERBOSE" == "1" ]]; then
    explore "${case_name}:after-eviction-deny"
  fi
  wait_ok "window Open (${case_name})" 90 window_phase Open || return 1
  wait_ok "scaled above ${baseline_replicas} (${case_name})" 120 replicas_gt "$baseline_replicas" || return 1

  local check
  for check in "${post_scale_checks[@]}"; do
    # shellcheck disable=SC2086
    wait_ok "$check" 90 $check || return 1
  done
  if [[ "$VERBOSE" == "1" ]]; then
    explore "${case_name}:scaled-up"
  fi

  wait_ok "spare Ready (${case_name})" 180 spare_ready || return 1
  if [[ "$VERBOSE" == "1" ]]; then
    explore "${case_name}:spare-ready"
  fi

  while true; do
    local NEXT
    NEXT="$(next_at_risk_pod "$NODE")"
    [[ -n "$NEXT" ]] || break
    expect_eviction_allowed "$NEXT" || return 1
    if [[ "$VERBOSE" == "1" ]]; then
      explore "${case_name}:after-evict-${NEXT}"
    fi
  done
  echo "ok  all at-risk pods on $NODE evicted (${case_name})"

  wait_ok "scaled back to ${baseline_replicas} (${case_name})" 180 replicas_are "$baseline_replicas" || return 1
  wait_ok "window Cooling or closed (${case_name})" 90 window_cooling_or_done || return 1
  if [[ "$VERBOSE" == "1" ]]; then
    explore "${case_name}:cooling-or-done"
  fi
  kubectl taint node "$NODE" karpenter.sh/disrupted:NoSchedule- || true
  wait_ok "window closed (${case_name})" 90 no_windows || return 1
  if [[ "$VERBOSE" == "1" ]]; then
    explore "${case_name}:final"
  fi
  echo "ok  case ${case_name} passed"
}

case_wanted() {
  local name="$1"
  [[ ",${CASES}," == *",${name},"* ]]
}

cleanup() {
  if [[ "$KEEP" == "1" ]]; then
    echo
    echo "KEEP=1: leaving cluster $CLUSTER (reuse next run with REUSE=1)"
    echo "  kind delete cluster --name $CLUSTER   # when done polishing"
    return
  fi
  echo "==> cleanup: deleting kind cluster $CLUSTER"
  kind delete cluster --name "$CLUSTER" >/dev/null 2>&1 || true
}
trap cleanup EXIT

load_image() {
  echo "==> load $IMG into kind"
  if ! kind load docker-image "$IMG" --name "$CLUSTER"; then
    echo "    kind load failed; retrying via docker save + ctr" >&2
    for _node in $(kind get nodes --name "$CLUSTER"); do
      docker save "$IMG" | docker exec -i "$_node" ctr --namespace=k8s.io images import --digests - >/dev/null
    done
  fi
}

kind_has_cluster() {
  kind get clusters 2>/dev/null | grep -qx "$CLUSTER"
}

ensure_cluster() {
  # REUSE=1: never delete; create only if missing. REUSE=0: fresh cluster each run.
  if kind_has_cluster; then
    if [[ "$REUSE" == "1" ]]; then
      echo "==> reusing kind cluster $CLUSTER"
      kubectl config use-context "kind-${CLUSTER}" >/dev/null 2>&1 || true
      return
    fi
    echo "==> kind cluster $CLUSTER exists; deleting for fresh run (REUSE=0)"
    kind delete cluster --name "$CLUSTER"
  elif [[ "$REUSE" == "1" ]]; then
    echo "==> no cluster $CLUSTER; creating (REUSE=1 will keep it)"
  else
    echo "==> kind cluster $CLUSTER (create)"
  fi
  kind create cluster --name "$CLUSTER" --config "$ROOT/test/e2e/kind-config.yaml"
  kubectl label node "${CLUSTER}-control-plane" eviction-guard.io/ignore=true --overwrite
  for n in $(kubectl get nodes -l '!node-role.kubernetes.io/control-plane' -o jsonpath='{.items[*].metadata.name}'); do
    kubectl label node "$n" karpenter.sh/capacity-type=spot --overwrite
  done
}

ensure_keda() {
  [[ "$NEED_KEDA" == "1" ]] || return 0
  if ! kubectl get deploy metrics-server -n kube-system >/dev/null 2>&1; then
    echo "==> install metrics-server (KEDA CPU triggers need it)"
    kubectl apply -f https://github.com/kubernetes-sigs/metrics-server/releases/download/v0.7.2/components.yaml
    kubectl -n kube-system patch deploy metrics-server --type=json \
      -p='[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--kubelet-insecure-tls"}]'
  fi
  wait_ok "metrics-server available" 180 \
    kubectl wait --for=condition=available deploy/metrics-server -n kube-system --timeout=150s

  if ! kubectl get deploy keda-operator -n keda >/dev/null 2>&1; then
    echo "==> install KEDA ${KEDA_CHART_VERSION}"
    helm upgrade --install keda keda \
      --repo https://kedacore.github.io/charts \
      --version "$KEDA_CHART_VERSION" \
      --namespace keda --create-namespace \
      --timeout 10m \
      --wait=false
  else
    echo "==> KEDA already installed"
  fi
  wait_ok "keda-operator available" 300 \
    kubectl wait --for=condition=available deploy/keda-operator -n keda --timeout=280s
}

ensure_prometheus() {
  [[ "$NEED_PROM" == "1" ]] || return 0
  echo "==> ensure Prometheus scrapes EVG metrics (Recipe B)"
  # Metrics Service lives in eviction-guard-system; create it before helm installs the chart.
  kubectl create namespace eviction-guard-system --dry-run=client -o yaml | kubectl apply -f -
  kubectl apply -f "$ROOT/test/e2e/prometheus.yaml"
  wait_ok "prometheus available" 180 \
    kubectl wait --for=condition=available deploy/prometheus -n default --timeout=150s
  # Confirm scrape target is up (best-effort; query may be empty until first window).
  wait_ok "prometheus ready HTTP" 60 \
    bash -c 'kubectl exec -n default deploy/prometheus -- wget -qO- http://127.0.0.1:9090/-/ready | grep -q Ready'
}

rollout_manager() {
  # Force pods to pick up a newly loaded :e2e tag (same tag, new digest).
  kubectl rollout restart deploy -n eviction-guard-system -l control-plane=controller-manager >/dev/null 2>&1 || true
  wait_ok "manager ready" 180 \
    kubectl wait --for=condition=available deploy -n eviction-guard-system -l control-plane=controller-manager --timeout=150s
  wait_ok "webhook endpoints" 60 webhook_ready
}

if [[ "$SKIP_BUILD" != "1" ]]; then
  echo "==> build $IMG"
  docker build --provenance=false --sbom=false -t "$IMG" "$ROOT"
else
  echo "==> SKIP_BUILD=1 (using existing $IMG)"
fi

ensure_cluster
load_image

NEED_KEDA=0
NEED_PROM=0
case_wanted keda && NEED_KEDA=1
case_wanted keda-external && NEED_KEDA=1 && NEED_PROM=1
ensure_keda
ensure_prometheus

echo "==> helm upgrade eviction-guard"
HELM_EXTRA=()
# Recipe A patches ScaledObject; Recipe B does not need that RBAC.
if case_wanted keda; then
  HELM_EXTRA+=(
    --set-json 'extraClusterRoleRules=[{"apiGroups":["keda.sh"],"resources":["scaledobjects"],"verbs":["get","list","watch","patch","update"]}]'
  )
fi
helm upgrade --install eviction-guard "$ROOT/charts/eviction-guard" \
  --namespace eviction-guard-system --create-namespace \
  --set image.repository="${IMG%%:*}" \
  --set image.tag="${IMG##*:}" \
  --set image.pullPolicy=IfNotPresent \
  --set leaderElect=false \
  "${HELM_EXTRA[@]}" \
  --wait --timeout 3m

rollout_manager

FAILED=0

run_case() {
  # Run outside of `if` so set -e applies inside run_disruption_loop.
  set +e
  run_disruption_loop "$@"
  local rc=$?
  set -e
  return "$rc"
}

if case_wanted deployment; then
  cleanup_case
  kubectl apply -f "$ROOT/test/e2e/policy.yaml"
  kubectl apply -f "$ROOT/test/e2e/workload.yaml"
  if ! run_case deployment 3; then
    FAILED=1
    dump_state "deployment-failed"
  fi
fi

if case_wanted hpa; then
  cleanup_case
  kubectl apply -f "$ROOT/test/e2e/policy-hpa-minmax.yaml"
  kubectl apply -f "$ROOT/test/e2e/workload-hpa-minmax.yaml"
  wait_ok "HPA web exists" 60 kubectl get hpa web -n default
  if ! run_case hpa-minmax 3 "hpa_min_gt 3" "hpa_max_gt 3"; then
    FAILED=1
    dump_state "hpa-failed"
  fi
fi

if case_wanted keda; then
  cleanup_case
  kubectl apply -f "$ROOT/test/e2e/policy-keda.yaml"
  kubectl apply -f "$ROOT/test/e2e/workload-keda.yaml"
  wait_ok "ScaledObject web exists" 60 kubectl get scaledobject web -n default
  # Operator creates an HPA; Ready condition can lag without metrics-apiserver.
  wait_ok "KEDA-managed HPA exists" 180 \
    bash -c 'kubectl get hpa -n default -o name 2>/dev/null | grep -q .'
  if ! run_case keda 3 "so_min_gt 3"; then
    FAILED=1
    dump_state "keda-failed"
  fi
fi

if case_wanted keda-external; then
  cleanup_case
  # Namespace + Prometheus may already exist; re-apply is idempotent.
  kubectl create namespace eviction-guard-system --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  kubectl apply -f "$ROOT/test/e2e/prometheus.yaml" >/dev/null
  kubectl apply -f "$ROOT/test/e2e/policy-keda-external.yaml"
  kubectl apply -f "$ROOT/test/e2e/workload-keda-external.yaml"
  wait_ok "ScaledObject web exists" 60 kubectl get scaledobject web -n default
  wait_ok "KEDA-managed HPA exists" 180 \
    bash -c 'kubectl get hpa -n default -o name 2>/dev/null | grep -q .'
  # SO floor stays at 3; KEDA raises Deploy replicas from evg_desired_replicas.
  if ! run_case keda-external 3 "so_min_is 3" "window_actions_empty"; then
    FAILED=1
    dump_state "keda-external-failed"
  fi
fi

if [[ "$FAILED" -ne 0 ]]; then
  echo "backends e2e FAILED" >&2
  exit 1
fi

echo
echo "backends e2e passed (cases=${CASES}): selected backends OK"
