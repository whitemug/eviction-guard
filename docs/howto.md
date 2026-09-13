# How-to

## Simulate a disruption

1. Opt in a Deployment and apply a policy that matches the node ([Configure](configure.md)).
2. Put load on the app if you want to see the throughput win.
3. Mark the node:

```bash
kubectl taint node <node> karpenter.sh/disrupted=:NoSchedule
# or: kubectl cordon <node>   # if NodeCordoned is enabled (default)
```

4. Watch:

```bash
kubectl get egw -A -w
kubectl get deploy <name> -o yaml | grep replicas
kubectl get pods -l eviction-guard.io/protected=true -o wide
```

5. Expect: replicas rise → `status.spareReady=true` → voluntary drain/Eviction of at-risk pods can proceed one-by-one → scale-back → Cooling → window gone.

Clear the taint when done (`karpenter.sh/disrupted:NoSchedule-`). Scale-back does **not** wait for the taint to clear once at-risk pods are gone.

Local full loop (`make test-kind`): taint → **pods/eviction deny** → scale → SpareReady → **eviction allow** (one at a time) → scale-back → close.

```bash
make test-kind
VERBOSE=1 make test-kind           # plain-English Policy/Window/Deploy story + YAML at each step
VERBOSE=1 KEEP=1 make test-kind    # verbose + leave the kind cluster up
make test-kind ARGS='--verbose --keep'
```

The script prints `kind e2e script rev=…` at startup — if you do not see that line, you are not running the current `test/e2e/kind.sh`.

## How eviction gating works

The hard gate is a validating webhook on **`pods/eviction`** (what `kubectl drain` and Karpenter use).

| Situation | Webhook |
|---|---|
| Pod not `protected` | Allow |
| Node not vulnerable / no matching policy | Allow |
| Vulnerable, no window or `SpareReady=false` | **Deny** (policy should be scaling; deferred workloads stay denied until a slot opens) |
| `SpareReady=true` | Allow the **lexicographically first** at-risk pod name; deny others until it is gone |
| `status.forcedCool` (`maxWindow`) | Allow (fail-open after the cap) |

`kubectl delete pod` does **not** go through this API — PDB and this webhook do not apply. That is normal Kubernetes.

Constants and decision logic: `pkg/evictgate`.

### Webhook outage

With `failurePolicy: Fail` (default), if the webhook is unreachable, **Eviction of opted-in pods fails**. Drains stick until the webhook recovers. Prefer fixing the webhook over setting `Ignore` unless you accept cold evictions during outages.

Disable only with full awareness:

```bash
helm upgrade ... --set webhook.enabled=false
```

That also disables annotation validation and eviction gating.

## Opt in an existing Deployment

```bash
kubectl patch deploy <name> --type=json -p='[
  {"op":"add","path":"/metadata/annotations/eviction-guard.io~1scale-backend","value":"deployment"},
  {"op":"add","path":"/spec/template/metadata/labels/eviction-guard.io~1protected","value":"true"}
]'
```

Rolling update applies the label to new pods. Ensure a policy `nodeFilter` covers the nodes those pods land on. The validating webhook requires `scale-backend` whenever the template is protected.

## Use HPA with Eviction Guard

Prefer the **HPA floor** when an HPA owns the Deployment (do not fight the scaler with `Deployment.replicas`):

```yaml
metadata:
  annotations:
    eviction-guard.io/scale-backend: hpa=my-hpa
```

The `hpa` key must exist on the owning policy's `spec.backends`. EVG raises `minReplicas` for the window and restores it to baseline on scale-back; the HPA keeps owning desired count under load.

If you still list Deployment (e.g. for SpareReady primary) **and** an HPA:

```yaml
# policy.spec.backends
deployment:
  apiVersion: apps/v1
  kind: Deployment
  patches:
    - path: spec.replicas
  skipDownscaling: true   # raise for spare; do not yank replicas back
hpa:
  apiVersion: autoscaling/v1
  kind: HorizontalPodAutoscaler
  patches:
    - path: spec.minReplicas
```

```yaml
# workload
metadata:
  annotations:
    eviction-guard.io/scale-backend: deployment,hpa=my-hpa
```

Without `skipDownscaling`, EVG restores every patched path to its baseline when the window closes.

GitOps (Argo/Flux) will revert those patches unless you ignore capacity fields — see [GitOps](gitops.md).

### Cross-namespace scale backends

`scale-backend` may target another namespace with `key=ns/name` (for example `hpa=platform/web-hpa`). That is intentional. Isolation is RBAC and who may annotate workloads / create Policies — not an in-operator same-namespace deny. See [Configure](configure.md) and [SECURITY](../SECURITY.md).

### Capacity apply failures (and `maxWindow` failover)

Catalog patches are Kind-agnostic. When the API rejects a capacity write (for example HPA admission when `minReplicas` would exceed `maxReplicas`, missing RBAC, conflict, or transient API-server pressure):

1. The Window **still opens** so eviction stays gated
2. Condition **`CapacityApplied=False`** with reason `Rejected` / `Forbidden` / `Conflict` / `ApplyFailed` / `Missing`
3. Status message notes the error and that eviction **fail-opens after `maxWindow`** (`ForcedCool`)
4. Metric `evg_capacity_apply_error=1` until a later apply succeeds or the window clears

Transient failures (throttling, conflicts, brief overload) are expected to succeed on a later reconcile under the **same** `maxWindow` clock — Eviction Guard does not fail-open early just because an apply failed. Tune `maxWindow` for how long you are willing to wait for spare before drains proceed unprotected — Spot policies often use **10–15m** instead of the **2h** default:

```yaml
spec:
  maxWindow: 15m   # force-cool if spare cannot land (e.g. capacity patch rejected)
```

Optional: list **both** floor and ceiling paths on a catalog entry if you *want* Eviction Guard to raise the ceiling with the spare target (raise-only when `current < desired`). That is an explicit operator choice — prefer leaving headroom and relying on `maxWindow` failover when a patch is rejected.

```yaml
backends:
  hpa:
    apiVersion: autoscaling/v1
    kind: HorizontalPodAutoscaler
    patches:
      - path: spec.minReplicas
      - path: spec.maxReplicas   # optional; only if you want EG to raise max
```

First *patched* key on `scale-backend` is SpareReady primary. Prefer scaler-only binds when an HPA/KEDA owns capacity. Ignore patched capacity fields in GitOps — [GitOps](gitops.md).

## Use KEDA instead of (or with) Deployment/HPA patches

See [KEDA](keda.md): either patch `ScaledObject.spec.minReplicaCount`, or declare an empty-patch catalog entry and drive KEDA from `evg_*` Prometheus metrics.

## Add a cloud-specific signal

1. Diff node YAML before vs during drain ([signals](signals.md)).
2. Add `customSignals` on the policy (no rebuild).
3. Keep early markers for head-start; cordon remains the late universal trigger when in defaults.

## Troubleshoot

| Symptom | Check |
|---|---|
| No window / no scale | Policy `nodeFilter`? Pod `protected`? Node signal present? `kubectl get egp -o yaml` status |
| Drain stuck / no spare | Webhook up? `SpareReady`? `CapacityApplied`? `maxWindow` / `ForcedCool`? Image pull / scheduling? |
| Scale thrash with GitOps | Argo/Flux reverting replicas — see [GitOps](gitops.md) |
| Scale on every cordon | Narrow `nodeFilter`, or omit `NodeCordoned` from an explicit `disruptionSignals` list |
| Eviction allowed with no spare | Webhook disabled? `failurePolicy: Ignore`? Pod not using Eviction API (`delete`)? |
| Deferred workloads never scale | `maxConcurrentWindows`; eviction stays denied until a window slot frees |
| Double reconcile / flapping | `leaderElect: false` with multiple replicas? |

Metrics: see [Metrics](metrics.md). Events on windows/policies: `ScaledUp`, `SpareReady`, `ScaleUpFailed`, `MaxWindowExceeded`, `ScaledBack`.

## Upgrade notes (hold PDB → webhook)

Older builds used a namespace hold PDB and `eviction-guard.io/held` / `released` labels. Current builds:

- Gate with the **eviction webhook** only  
- `protected` = opt-in  
- You may delete leftover `eviction-guard-protected` PDBs if present  

Next: [Configure](configure.md) · [KEDA](keda.md) · [GitOps](gitops.md) · [Signals](signals.md) · [Design](design.md)
