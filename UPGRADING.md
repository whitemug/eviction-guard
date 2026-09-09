# Upgrading

`api/v1alpha1` is alpha. Treat chart `0.2.x` as a **new install contract**, not an in-place compatible bump from `0.1.x`.

## From 0.2.0 → 0.2.1

In-place chart/image upgrade is supported (same CRD API).

1. Upgrade to chart/image **0.2.1** (includes the eviction webhook fail-open fix and related gate/window restore fixes).
2. No Policy/Workload annotation changes required for this bump.
3. If you still run `0.2.0`, prefer upgrading before relying on the gate under API/cache errors — older builds could allow eviction when pod `Get` failed for non-NotFound reasons.

## From 0.1.x → 0.2.0

1. **Drain / quiet the cluster** (or accept that open windows will scale back when the old controller stops).
2. Remove old Policies / Windows if they still use removed fields (`defaultBackend`, Window `spec.backend`, REST `scale-target`).
3. Upgrade the chart/image to `0.2.x` (CRDs ship in the chart `crds/` folder).
4. Re-apply Policies with required `spec.backends`, for example:

```yaml
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
```

5. On Deployments:
   - Keep `eviction-guard.io/protected: "true"` on the pod template.
   - Rename pin annotation `eviction-guard.io/policy` → `eviction-guard.io/policy-pin` if you used pinning.
   - **Always** set `eviction-guard.io/scale-backend` (required). Example: `deployment` or `deployment,hpa`.
     Token order is patch order (scale-up and scale-back); the first key is the SpareReady primary.
     There is no default Deployment bind when the annotation is omitted.
6. Grant `extraClusterRoleRules` for custom catalog GVKs.
7. Confirm the validating webhook is present and `failurePolicy` is intentional (`Fail` blocks drains of opted-in pods if the webhook is down).

## Compatibility promise (alpha)

| Stable until we say otherwise | May change before v1beta1 |
|---|---|
| Label/annotation **strings** in `api/v1alpha1/labels.go` (after 0.2.0 renames) | CRD field shapes inside `v1alpha1` |
| Metric names (`evg_*`) | Go package layouts under `pkg/` except listed contracts |
| `pkg/plugin.RegisterSignal`, `pkg/signals.Detector`, `pkg/evictgate`, `pkg/policyown`, catalog helpers in `pkg/backends` | Internal packages and unexported helpers |

See [CONTRIBUTING.md](CONTRIBUTING.md) and [CHANGELOG.md](CHANGELOG.md).
