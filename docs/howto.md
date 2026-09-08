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

```yaml
metadata:
  annotations:
    eviction-guard.io/scale-backend: deployment,hpa=my-hpa
```

The `hpa` key must exist on the owning policy's `spec.backends`. List every backend you want patched — there is no default bind. Put `deployment` first (SpareReady primary). EVG raises `minReplicas` for the window and restores it on scale-back without fighting an HPA that has already moved *above* the spare.

GitOps (Argo/Flux) will revert those patches unless you ignore capacity fields — see [GitOps](gitops.md).

### HPA `minReplicas` == `maxReplicas`

Eviction Guard can patch **both** on one catalog entry. Paths are applied
**top-to-bottom** in list order; consecutive paths on the same object are written
in a single merge patch (so `min`/`max` stay valid together).

```yaml
backends:
  hpa:
    apiVersion: autoscaling/v1
    kind: HorizontalPodAutoscaler
    patches:
      - path: spec.minReplicas
      - path: spec.maxReplicas
```

Each path becomes its own Window action (own baseline); both are set to the shared desired capacity. That way `min == max == 5` can become `6`/`6` so a spare can schedule.

If you only patch `minReplicas` and leave `maxReplicas` at 5, HPA cannot run more than 5 pods → `SpareReady` stays false → eviction stays denied until you raise max, free capacity, or hit `maxWindow` (`ForcedCool`).

Leave headroom or patch both paths. Keep `deployment` first on `scale-backend` for SpareReady. Ignore both HPA fields in GitOps — [GitOps](gitops.md).

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
| Drain stuck / no spare | Webhook up? `SpareReady`? HPA `maxReplicas` headroom? `maxWindow` / `ForcedCool`? Image pull / scheduling? |
| Scale thrash with GitOps | Argo/Flux reverting replicas — see [GitOps](gitops.md) |
| Scale on every cordon | Narrow `nodeFilter`, or omit `NodeCordoned` from an explicit `disruptionSignals` list |
| Eviction allowed with no spare | Webhook disabled? `failurePolicy: Ignore`? Pod not using Eviction API (`delete`)? |
| Deferred workloads never scale | `maxConcurrentWindows`; eviction stays denied until a window slot frees |
| Double reconcile / flapping | `leaderElect: false` with multiple replicas? |

Metrics: see [Metrics](metrics.md). Events on windows/policies: `ScaledUp`, `SpareReady`, `MaxWindowExceeded`, `ScaledBack`.

## Upgrade notes (hold PDB → webhook)

Older builds used a namespace hold PDB and `eviction-guard.io/held` / `released` labels. Current builds:

- Gate with the **eviction webhook** only  
- `protected` = opt-in  
- You may delete leftover `eviction-guard-protected` PDBs if present  

Next: [Configure](configure.md) · [KEDA](keda.md) · [GitOps](gitops.md) · [Signals](signals.md) · [Design](design.md)
