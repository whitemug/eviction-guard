# Configure

Three layers: **policy** (which nodes / how to scale), **workload opt-in** (which Deployments), **signals** (when a node is vulnerable).

## 1. EvictionGuardPolicy

Cluster-scoped. Empty `nodeFilter` = all nodes. Prefer one policy per pool (Spot, upgrade pool, …).

```yaml
apiVersion: eviction-guard.io/v1alpha1
kind: EvictionGuardPolicy
metadata:
  name: spot-workers
spec:
  nodeFilter:
    capacityTypes: ["spot"]
    labelSelector:
      matchLabels:
        karpenter.sh/nodepool: default
    excludeLabelSelector:
      matchLabels:
        eviction-guard.io/ignore: "true"
    zones: ["us-east-1a"]          # optional
    namePattern: "ip-10-0-*"       # optional glob
  spareReplicas: 1
  maxBuffer: 4
  maxConcurrentWindows: 8          # 0 = unlimited
  maxWindow: 2h                    # 0 = unlimited; then ForcedCool
  backends:
    deployment:
      apiVersion: apps/v1
      kind: Deployment
      patches:
        - path: spec.replicas
    hpa:
      apiVersion: autoscaling/v1
      kind: HorizontalPodAutoscaler
      patches:
        - path: spec.minReplicas
  scaleBackAfter: 1m
  # disruptionSignals: []          # empty = built-in defaults
  # customSignals: []              # see signals.md
```

### `nodeFilter` fields (ANDed)

| Field | Purpose |
|---|---|
| `labelSelector` / `excludeLabelSelector` | Node labels |
| `names` | Explicit allow-list |
| `namePattern` | Glob (`filepath.Match`) |
| `zones` | `topology.kubernetes.io/zone` |
| `instanceTypes` | `node.kubernetes.io/instance-type` |
| `capacityTypes` | `karpenter.sh/capacity-type` |
| `taintSelector` | Node must have listed taints |

Also optional: `namespaceSelector`, `workloadSelector` to limit which namespaces / Deployments a policy may scale.

Prefer **one policy per workload class** (e.g. aggressive spare for web, lazy for batch) via `workloadSelector`. If more than one policy still matches a Deployment:

1. Annotation `eviction-guard.io/policy-pin: <policy-name>` on the Deployment **pins** that policy as owner (ignores `namespaceSelector` / `workloadSelector`; `nodeFilter` and signals still apply). If the named policy does not exist, no other policy owns the workload.
2. Otherwise the lexicographically **first matching policy name** wins.

Do not rename a policy while its windows are Open — Kubernetes replace is delete+create and existing windows scale back (see [Scale targets design note](design-scale-targets.md)).

### Defaults when `disruptionSignals` is omitted

`KarpenterDisrupted`, `KarpenterDeleteRequested`, `OutOfService`, `NodeCordoned`.

`NodeCordoned` covers `kubectl drain` / NTH cordon. Narrow with `nodeFilter` if cordons elsewhere are noisy. Omit it from an **explicit** list if you do not want cordon-only triggers. Full list: [Signal catalog](signals.md).

### Capacity knobs

| Field | Meaning |
|---|---|
| `spareReplicas` | Extra replicas per window (clamped by `maxBuffer`) |
| `maxConcurrentWindows` | Cap Open/Cooling/Held windows; extras wait (eviction still denied until a window + spare) |
| `maxWindow` | Force-cool Open windows that last this long (`ForcedCool` → webhook allows eviction) |
| `scaleBackAfter` | Cooling duration after scale-back |
| `backends` | Named catalog: key → apiVersion/kind/patches (include `deployment` + `hpa` in examples) |

`spec.backends` is required. Protected Deployments must set `eviction-guard.io/scale-backend` to list catalog keys (optional `key=name` / `key=ns/name`; bare key uses the Deployment’s name). There is **no default bind** — without the annotation, Eviction Guard does not scale. **Token order on the annotation is patch order** for entries with integer paths; the first *patched* path is the SpareReady primary (`actions[0]`). Put the Deployment capacity key first when you patch it (for example `deployment,hpa`). A single catalog entry may list **multiple integer paths** (for example HPA `minReplicas` and `maxReplicas`); each path gets its own Window action and baseline. An entry may omit patches entirely for an **external** scaler (Window + metrics only) — see [KEDA](keda.md).

Examples: `examples/policy-spot.yaml`, `policy-cordon.yaml`, `policy-custom-signals.yaml`, `policy-named-pool.yaml`, `policy-keda-*.yaml`.

## 2. Workload opt-in

On the **Deployment** (annotation required) and **pod template** (protected label):

```yaml
metadata:
  annotations:
    eviction-guard.io/scale-backend: deployment   # required; add ,hpa=web for HPA
spec:
  template:
    metadata:
      labels:
        eviction-guard.io/protected: "true"
```

Additional Deployment annotations:

```yaml
metadata:
  annotations:
    eviction-guard.io/scale-backend: deployment,hpa=web,webapp=fireship  # required
    eviction-guard.io/scale-back-after: 20m
    eviction-guard.io/stamp: "true"
    # eviction-guard.io/policy-pin: web-aggressive         # pin owning policy
```

| Annotation | Purpose |
|---|---|
| `scale-backend` | **Required** on protected Deployments: catalog keys (+ optional `=name` / `=ns/name`); first *patched* key is SpareReady primary |
| `scale-back-after` | Override policy cooldown |
| `stamp` / `stamp-*` | Visibility annotations on scaled objects |
| `policy-pin` | Pin owning `EvictionGuardPolicy` by name (see §1) |

The validating webhook rejects protected Deployments without `scale-backend`, and malformed annotations, at apply time. Full CR example: `examples/workload-multi-backend.yaml` (with `examples/policy-custom-backend.yaml` for an app CR).

## 3. What you observe

`EvictionGuardWindow` in the workload namespace:

- `spec.baseline` / `spec.scaledTo` / `spec.vulnerableNodes`
- `status.spareReady` — enough Ready pods **off** dying nodes
- `status.phase`: `Open` → `Cooling` → `Closed` (or `Held` while HPA desired/current keeps capacity above the spare target — still counts toward `maxConcurrentWindows`)
- `status.forcedCool` — `maxWindow` expired; webhook fail-opens

Deleting a policy garbage-collects its windows after scale-back (finalizer).

## 4. Chart `defaultPolicy`

```bash
helm install ... \
  --set defaultPolicy.enabled=true \
  --set defaultPolicy.nodeFilter.capacityTypes[0]=spot
```

Prefer explicit `examples/` manifests in GitOps. If Argo/Flux manages the same Deployments/HPAs, configure capacity ignore so sync does not undo open windows — [GitOps](gitops.md). For KEDA-owned capacity, see [KEDA](keda.md).

Next: [How-to](howto.md) · [KEDA](keda.md) · [GitOps](gitops.md) · [Signal catalog](signals.md) · [Extension](extension.md)
