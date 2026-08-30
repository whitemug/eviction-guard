# Extension guide — using Eviction Guard from other Kubernetes solutions

Eviction Guard is built so **node coverage and capacity mutation are not hardcoded**. Other controllers, Helm charts, and GitOps layouts should integrate without forking.

## 1. CRD integration (no Go import)

This is the path for most operators.

**Select nodes** by applying an `EvictionGuardPolicy` with `spec.nodeFilter`. Filters are ANDed. Empty filter = all nodes. Create one policy per pool you care about.

**Add a newly discovered disruption signal** on the same policy — no rebuild. See [Identifying a disruption signal](#identifying-a-disruption-signal) below.

| Field | Purpose |
|---|---|
| `labelSelector` / `excludeLabelSelector` | Standard node labels |
| `names` | Explicit allow-list |
| `namePattern` | Glob (`filepath.Match`) |
| `zones` | `topology.kubernetes.io/zone` |
| `instanceTypes` | `node.kubernetes.io/instance-type` |
| `capacityTypes` | `karpenter.sh/capacity-type` (`spot`, `on-demand`) |
| `taintSelector` | Node must have the listed taints |

`spec.maxConcurrentWindows` (default 8, `0` = unlimited) caps how many Open/Cooling/Held windows one policy may hold. Extra at-risk workloads wait until a window closes. `spec.maxWindow` (default 2h, `0` = unlimited) force-cools a window that stays Open too long.

**Observe actions** by watching `ProactiveWindow` in the workload namespace:

- `spec.baseline` / `spec.scaledTo` / `spec.backend` (primary / kubectl columns)
- `spec.actions[]` — every object patched (Deployment, HPA, CR), each with its own baseline
- `spec.vulnerableNodes`
- `spec.windowUntil` — set only after vulnerable nodes have cleared **and** `status.spareReady` (Cooling); empty while Open
- `status.phase`: `Open` → `Cooling` → `Held` | `Closed`

Owner reference: windows are controlled by the policy, so deleting the policy garbage-collects windows (after the window finalizer restores capacity).

**Constants** (do not fork the strings): see `api/v1alpha1/labels.go`. Helm’s validating webhook rejects unknown `scale-backend` values, an unparseable `scale-target` / `scale-back-after`, and `crd` without a full GVK target at apply time.

```
eviction-guard.io/enabled: "true"
eviction-guard.io/scale-backend: deployment|hpa-min|crd   # comma-separated list is allowed
eviction-guard.io/hpa-target: <ns/name>                   # HPA to raise; defaults to HPA named like the Deployment
eviction-guard.io/scale-target: <group/version/namespaces/ns/kind/name>
eviction-guard.io/crd-replicas-path: spec.replicas
eviction-guard.io/scale-back-after: 20m                   # overrides policy spec.scaleBackAfter
eviction-guard.io/stamp: "true"                           # write values onto Deployment, HPA, and/or CR
eviction-guard.io/stamp-scaled-to: example.com/desired    # optional key names
eviction-guard.io/stamp-baseline: example.com/baseline
eviction-guard.io/stamp-active: example.com/eg-active
eviction-guard.io/stamp-window-until: example.com/eg-until
```

## 2. Make *your* CR the capacity knob

If your operator already reconciles replica count from a CR:

```yaml
metadata:
  labels:
    eviction-guard.io/enabled: "true"
  annotations:
    eviction-guard.io/scale-backend: crd
    eviction-guard.io/scale-target: example.com/v1/namespaces/app/Widget/web
    eviction-guard.io/crd-replicas-path: spec.replicas
    eviction-guard.io/stamp: "true"
```

Eviction Guard patches that integer field. When `stamp` is true (or policy `spec.stamp.enabled`), it also writes visibility annotations on **every** scaled object (Deployment, HPA, and this CR):

```
eviction-guard.io/scaled-to: "4"
eviction-guard.io/baseline: "3"
eviction-guard.io/active: "true"
```

`eviction-guard.io/window-until` is written later, when cooldown starts (`spec.windowUntil` is set, after nodes clear and SpareReady). Keys are configurable (`spec.stamp.scaledToKey` or `eviction-guard.io/stamp-scaled-to` on the Deployment). Scale-back deletes those annotations and restores the field.

Your controller keeps owning topology, rollout, and mesh weights. Grant Eviction Guard RBAC to patch that CR (`extraClusterRoleRules` in the Helm chart).

## 3. Go plugins (same process)

Import the module and register in `init()` before the manager starts (or fork `cmd/main.go` in a thin wrapper binary):

```go
package myplugin

import (
    "github.com/whitemug/eviction-guard/pkg/plugin"
    "github.com/whitemug/eviction-guard/pkg/backends"
    "github.com/whitemug/eviction-guard/pkg/signals"
    "github.com/whitemug/eviction-guard/pkg/filters"
)

func init() {
    plugin.RegisterBackend(myBackend{})     // backends.Backend
    plugin.RegisterSignal(myDetector{})     // signals.Detector
    plugin.RegisterFilter("gpu-only", myFilterFactory) // filters.Factory
}
```

Interfaces:

- `backends.Backend` — `Current`, `ScaleUp`, `ScaleDown`
- `signals.Detector` — `Name() DisruptionSignal`, `Vulnerable(*Node) bool`
- `filters.Filter` — `Matches(*Node) (bool, error)`

Built-in signals: `KarpenterDisrupted`, `KarpenterDeleteRequested`, `OutOfService`, `SpotInterrupted`, `NodeCordoned` (opt-in). Cloud-specific recipes (GKE / AKS / Cluster Autoscaler) live in [`docs/06-signal-catalog.md`](06-signal-catalog.md) as `customSignals` — prefer that over new compiled enums.

## 4. Identifying a disruption signal

A Node field is worth adding if **all** of these are true:

1. **It lives on the Node** (taint, annotation, or label) — not a Kubernetes Event, not a cloud API you would have to poll.
2. **It is written before drain** — there is lead time for replacement pods to become Ready.
3. **It is specific to impending eviction** — not a generic health flag (`Ready=False`, DiskPressure) and not a standing identity label (`karpenter.sh/capacity-type=spot`).
4. **It clears when the node is done** — so the window controller can scale back.

How to find one: cordon/drain a node in a test pool (or wait for a real rotation) and diff `kubectl get node <n> -o yaml` before vs after. The new taint/annotation/label is the signal.

Then add it to the policy. It is ORed with the built-in list:

```yaml
spec:
  disruptionSignals:          # built-in names only
    - KarpenterDisrupted
    - OutOfService
  customSignals:              # anything you discovered
    - name: GKEImpendingTermination
      taint:
        key: cloud.google.com/impending-node-termination
    - name: GKEMaintenanceOngoing
      label:
        key: cloud.google.com/active-node-maintenance
        value: ONGOING
    - name: AKSUpgradeQuarantined
      label:
        key: kubernetes.azure.com/upgrade-status
        value: Quarantined
    - name: ClusterAutoscalerToBeDeleted
      taint:
        key: ToBeDeletedByClusterAutoscaler
```

See `examples/policy-custom-signals.yaml` and the [signal catalog](06-signal-catalog.md). Promote a custom signal to a built-in enum only after it is widely used.

## 5. What not to do

- Do not run a competing scaler that also patches `spec.replicas` during a disruption window.
- Do not reuse `kubernetes.io/*` labels; all public keys live under `eviction-guard.io/`.
- Do not treat `v1alpha1` as frozen until a v1beta1 / v1 announcement.
