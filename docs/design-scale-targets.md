# Design note — scale targets beyond Deployment

Status: **decisions locked for 0.2.0** (multi-policy ownership + backend catalog shipped). This is design rationale, not a day-2 operator guide — see [Configure](configure.md) and [Extension](extension.md). Core architecture: [Design](design.md).

## Problem

Eviction Guard’s workload unit is a **Deployment**:

1. `protected` pods → owner walk → Deployment  
2. `EvictionGuardWindow` targets that Deployment  
3. Capacity patches fan out via a **Policy backend catalog** to Deployment replicas, HPA floors, app CRs, KEDA ScaledObjects, …

Earlier, CR wiring used a REST-path annotation (`scale-target`) and a single `crd` slot. That was awkward vs HPA/KEDA (`apiVersion` + `kind` + `name`), put GVK on every Deployment, and could not patch two different CRs.

## How peers do it

| Project | Pattern |
|---|---|
| **HPA** | Structured `scaleTargetRef`: `apiVersion`, `kind`, `name` (same namespace) |
| **KEDA** | Same on `ScaledObject.spec.scaleTargetRef` |
| **OpenKruise** | Same `scaleTargetRef` when HPA targets CloneSet / UnitedDeployment |

Nobody major encodes user-facing refs as `group/version/namespaces/ns/kind/name`. Same-namespace default is the norm.

## Motivating scenario

```text
WebApp (tracking / app UX)
    └── ScaledObject (KEDA) ──► HPA ──► pods
```

Desired during a disruption window:

- Raise `ScaledObject.spec.minReplicaCount` to the shared pod target (if KEDA owns the HPA, do **not** also patch the HPA), **or**
- Omit ScaledObject path patches (external): open a Window + publish `evg_*` metrics and let KEDA scale from Prometheus — [KEDA](keda.md)

## Shipped direction

**Policy** defines named backends (how to talk to a type of object).  
**Deployment** only binds local names to those keys.

```yaml
apiVersion: eviction-guard.io/v1alpha1
kind: EvictionGuardPolicy
metadata:
  name: spot-workers
spec:
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
    webapp:
      apiVersion: example.com/v1
      kind: WebApp
      patches:
        - path: spec.replicas
    scaledobject:
      apiVersion: keda.sh/v1alpha1
      kind: ScaledObject
      patches:
        - path: spec.minReplicaCount
    # Or omit patches for external / metrics-only ScaledObject.
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: firework
  annotations:
    eviction-guard.io/scale-backend: deployment,hpa,webapp=firework,scaledobject=firework-keda-scaler
spec:
  template:
    metadata:
      labels:
        eviction-guard.io/protected: "true"
```

### Defaults

- `eviction-guard.io/scale-backend` is **required** on protected Deployments; there is no implicit Deployment-kind bind.
- Bare names on the annotation default to the Deployment’s namespace and name; optional `ns/name` for cross-namespace.
- Token order is patch order for entries with integer paths; the first *patched* path is the SpareReady primary. Empty-patch entries are external (no Window actions).

### Follow-ups (non-blocking)

1. **Admission depth** — Catalog keys are only valid relative to a Policy; full cross-object validation at Deployment apply time needs the owning Policy. Reconcile emits events / Policy conditions on unknown keys. Syntax (and required `scale-backend` when protected) is already rejected by the webhook.
2. Pin annotation is `eviction-guard.io/policy-pin` (distinct from Window label `eviction-guard.io/policy`).

## Decisions

### 1. Per-backend desired math — decided

Shared **pod capacity target**: compute `desired` once; every bound path is set to that absolute value. Each action keeps its own **baseline** for restore. If KEDA owns the HPA, patch ScaledObject only.

### 2. Track vs scale / `mode` — decided (dropped)

No `mode`. Fan-out for speed: Deployment + floor owner + app CR as needed.

### 3. Multi-policy ownership — decided

Pin annotation or lexicographically first matching Policy name. See [Configure](configure.md).

### 4. Backend catalog + one annotation — decided

- Required `policy.spec.backends`  
- Required workload annotation: `eviction-guard.io/scale-backend`  
- Window `ScaleAction.key` stores the catalog key; patch order follows the annotation token order
  for entries with integer paths. `actions[0]` is the SpareReady primary when Actions is non-empty.
  Empty-patch catalog entries open a Window with no actions (external scaler). 

## Conclusions

1. **GVK + path live on the Policy catalog.**  
2. **Deployment remains the place for pod opt-in (`protected`).**  
3. **One required workload annotation** lists catalog keys (+ optional names) for every scale target.  
4. **No implicit default bind** — omit the annotation and Eviction Guard does not scale.  
5. **Multi-policy ownership** via selectors, first-by-name, or pin.

## Out of scope for this note

- Cluster-global BackendCatalog CR  
- StatefulSet / non-Deployment primary workloads  
- Using `/scale` subresource instead of a JSON path  
- Tracking-only objects that are never patched
