# Extension — integrate from other Kubernetes solutions

Eviction Guard keeps **node coverage** and **capacity mutation** pluggable. Most operators only need CRDs; Go plugins are for custom disruption signals.

For day-2 policy and workload settings see [Configure](configure.md). Scale-target design: [Scale targets](design-scale-targets.md).

## 1. CRD integration (no Go)

1. Apply an `EvictionGuardPolicy` with `nodeFilter`, required `backends`, and optional `customSignals`.
2. Opt workloads in with `eviction-guard.io/protected: "true"`.
3. Watch `EvictionGuardWindow` for `baseline`, `scaledTo`, `spareReady`, `phase`, and `spec.actions`.

Owner reference: deleting a policy garbage-collects windows after scale-back.

Constants: `api/v1alpha1/labels.go`. The validating webhook rejects protected Deployments without `scale-backend`, bad `scale-backend` / `policy-pin` syntax, and invalid Policy fields at apply time.

## 2. Make *your* CR the capacity knob

Add a catalog entry on the Policy, then reference it from the workload:

```yaml
# on EvictionGuardPolicy.spec.backends
webapp:
  apiVersion: example.com/v1
  kind: Widget
  patches:
    - path: spec.replicas
---
# on the Deployment
metadata:
  annotations:
    eviction-guard.io/scale-backend: deployment,webapp=fireship
    eviction-guard.io/stamp: "true"
spec:
  template:
    metadata:
      labels:
        eviction-guard.io/protected: "true"
```

Eviction Guard patches that integer field (and optional stamp annotations) with an unstructured merge-patch. Your controller keeps topology / rollout. Grant RBAC via Helm `extraClusterRoleRules` for the CR’s API group. If RBAC is missing, scale-up fails with Events and Policy condition `BackendsAuthorized=False` (`MissingRBAC`).

See `examples/policy-custom-backend.yaml` and `examples/workload-multi-backend.yaml`.

## 3. Go plugins (same process)

Capacity is **not** a Go plugin — use the Policy catalog. Node coverage uses `spec.nodeFilter` / `customSignals`. Register custom disruption detectors in `init()` before the manager starts:

```go
package myplugin

import (
    "github.com/whitemug/eviction-guard/pkg/plugin"
    "github.com/whitemug/eviction-guard/pkg/signals"
)

func init() {
    plugin.RegisterSignal(myDetector{})
}
```

- `signals.Detector` — `Name() DisruptionSignal`, `Vulnerable(*Node) bool`

Prefer [customSignals](signals.md) over new compiled enums until a marker is widely used.

Eviction admission decisions live in `pkg/evictgate` (used by the webhook). Ownership rules live in `pkg/policyown`.

## Identifying a disruption signal

A Node field is worth adding if **all** are true:

1. **On the Node** (taint, annotation, or label) — not an Event or cloud API poll.
2. **Written before drain** — lead time for Ready spares.
3. **Specific to impending eviction** — not `Ready=False` / DiskPressure / standing identity labels.
4. **Clears when done** — so windows can close.

Diff `kubectl get node <n> -o yaml` before vs during a rotation, then add `customSignals` (ORed with built-ins). See [signals](signals.md) and `examples/policy-custom-signals.yaml`.

## What not to do

- Do not run a competing scaler that also patches `spec.replicas` during an open window.
- Do not reuse `kubernetes.io/*` labels; public keys are under `eviction-guard.io/`.
- Do not treat `v1alpha1` as frozen until a v1beta1 / v1 announcement.
- Do not disable the eviction webhook unless you accept ungated voluntary drains for opted-in pods.
- Do not add a catalog GVK without granting the manager `get/list/watch/patch` on that resource.
