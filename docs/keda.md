# KEDA

Two supported recipes when KEDA owns scaling. Eviction Guard still opens an
`EvictionGuardWindow`, gates `pods/eviction` until SpareReady, and publishes
`evg_at_risk_pods` / `evg_desired_replicas`. Choose whether EVG patches the
ScaledObject or leaves capacity entirely to KEDA.

## Recipe A — patch `ScaledObject.spec.minReplicaCount`

Use when KEDA owns the HPA and you want EVG to raise the floor for the window
(same idea as patching HPA `minReplicas`).

**Do not** also patch the generated HPA.

```yaml
# Policy catalog excerpt
backends:
  scaledobject:
    apiVersion: keda.sh/v1alpha1
    kind: ScaledObject
    patches:
      - path: spec.minReplicaCount
```

Workload:

```yaml
metadata:
  annotations:
    eviction-guard.io/scale-backend: scaledobject=my-so
```

Grant manager RBAC (Helm):

```yaml
extraClusterRoleRules:
  - apiGroups: ["keda.sh"]
    resources: ["scaledobjects"]
    verbs: ["get", "list", "watch", "patch", "update"]
```

Ignore `spec.minReplicaCount` in GitOps while windows can be open — [GitOps](gitops.md).

Examples: [`examples/policy-keda-minreplicas.yaml`](../examples/policy-keda-minreplicas.yaml),
[`examples/workload-keda.yaml`](../examples/workload-keda.yaml).

## Recipe B — external (empty patches) + Prometheus triggers

Use when GitOps / KEDA must own replica count and EVG must not patch anything.
Catalog entry with **no path patches** is declared on `scale-backend` but skipped
for capacity writes. Window `spec.actions` stays empty; `spec.baseline` is the
Deployment replica count at open (sticky for the window). SpareReady still uses
Ready pods off vulnerable nodes vs that baseline — KEDA must raise replicas via
metrics so spare can become Ready.

```yaml
backends:
  scaledobject:
    apiVersion: keda.sh/v1alpha1
    kind: ScaledObject
    # no patches — external
```

```yaml
metadata:
  annotations:
    eviction-guard.io/scale-backend: scaledobject=my-so
```

No ScaledObject patch RBAC is required for Recipe B (EVG does not touch the object).
Wire KEDA Prometheus triggers to EVG metrics, for example:

- Presence: `evg_at_risk_pods{workload="…"} > 0` or `evg_spare_not_ready == 1`
- Target size: `evg_desired_replicas{workload="…"}`

If the ScaledObject already has a queue (or other) trigger, add EVG as another
trigger. KEDA takes the **max** across triggers (not a sum) unless you configure
otherwise — size the EVG target so it can win during disruption.

SpareReady still depends on KEDA (or another controller) raising Ready pods.
Until then, eviction stays denied — the same as a stuck spare. The hard safety
valve is still **`maxWindow` → `ForcedCool`** (fail-open); there is no separate
external-window fail-open path. Tune `maxWindow` for how long you will wait for
metrics-driven spare (Spot often **10–15m**). Watch `SpareReady`,
`evg_spare_not_ready`, and window Events if drains stall.

Examples: [`examples/policy-keda-external.yaml`](../examples/policy-keda-external.yaml),
[`examples/scaledobject-evg-metrics.yaml`](../examples/scaledobject-evg-metrics.yaml).

## Which to pick

| | Recipe A (patch min) | Recipe B (external + metrics) |
|---|---|---|
| Who raises replicas | EVG patches ScaledObject | KEDA from Prometheus |
| GitOps noise | Ignore `minReplicaCount` | None from EVG |
| Chart RBAC | Need `extraClusterRoleRules` | Not for ScaledObject |
| Queue + EVG | Prefer Recipe B (multiple triggers) | Natural fit |

Next: [Metrics](metrics.md) · [Configure](configure.md) · [GitOps](gitops.md).
