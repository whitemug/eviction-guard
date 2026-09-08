# Disruption signal catalog

Eviction Guard has two layers:

1. **Built-in names** in `spec.disruptionSignals` (compiled detectors).
2. **Recipes** in `spec.customSignals` (taint / annotation / label matchers). Add these without a new release.

A marker belongs here only if it is on the Node, appears *before* drain, means impending eviction, and clears afterwards. Confirm with `kubectl get node <n> -o yaml` before vs during a rotation.

Early signals **accelerate** scale-up. The **`pods/eviction` webhook** is what sequences drain after spare Ready. See [How-to](howto.md).

## Built-in (`disruptionSignals`)

| Name | Marker | Default? |
|---|---|---|
| `KarpenterDisrupted` | Taint `karpenter.sh/disrupted` | yes |
| `KarpenterDeleteRequested` | Annotation `karpenter.sh/delete-requested-at` | yes |
| `OutOfService` | Taint `node.kubernetes.io/out-of-service` | yes |
| `SpotInterrupted` | Spot annotation / `spotinst.com/volatile` / out-of-service | no |
| `NodeCordoned` | `spec.unschedulable: true` | **yes** (drain prelude; scope with `nodeFilter`) |

```yaml
spec:
  disruptionSignals:
    - KarpenterDisrupted
    - KarpenterDeleteRequested
    - OutOfService
    - NodeCordoned
```

Omit `NodeCordoned` from an explicit list if cordons in your cluster are often unrelated to drain. Prefer `nodeFilter` to limit which pools react.

## GKE (`customSignals`)

| Recipe name | Kind | Key | Notes |
|---|---|---|---|
| `GKEImpendingTermination` | taint | `cloud.google.com/impending-node-termination` | Host maintenance / GPU-TPU drain |
| `GKEMaintenanceOngoing` | label | `cloud.google.com/active-node-maintenance=ONGOING` | Workloads being stopped |
| `GKEMaintenanceWindow` | taint | `cloud.google.com/maintenance-window-started` | Scheduled maintenance window |

Standard node-pool **upgrades** often only cordon+drain. `NodeCordoned` is on by default; narrow with `nodeFilter`, or wait for maintenance taints if you omit cordon from an explicit list.

## AKS (`customSignals`)

| Recipe name | Kind | Key | Notes |
|---|---|---|---|
| `AKSUpgradeQuarantined` | label | `kubernetes.azure.com/upgrade-status=Quarantined` | Undrainable node during rolling upgrade |
| `AKSSpotEviction` | taint | `kubernetes.azure.com/scalesetpriority` is identity, **not** a signal | Do not use as a disruption signal |

AKS rolling upgrades cordon then drain. Prefer `NodeCordoned` on the upgrade node pool (`nodeFilter.labelSelector`) rather than every cordon in the cluster.

## Cluster Autoscaler (GKE / EKS / AKS)

| Recipe name | Kind | Key | Notes |
|---|---|---|---|
| `ClusterAutoscalerToBeDeleted` | taint | `ToBeDeletedByClusterAutoscaler` | Scale-down decided (NoSchedule) |
| `ClusterAutoscalerCandidate` | taint | `DeletionCandidateOfClusterAutoscaler` | Soft; CA may still back off |

## EKS + Karpenter

Prefer built-ins (`KarpenterDisrupted`, `KarpenterDeleteRequested`, `NodeCordoned`). AWS NTH typically cordons — covered by default `NodeCordoned` / `OutOfService`. Spot rebalance is often NTH-only — add whatever taint/annotation NTH writes via `customSignals` for earlier notice than cordon.

Copy-paste: [`examples/policy-custom-signals.yaml`](../examples/policy-custom-signals.yaml), [`examples/policy-cordon.yaml`](../examples/policy-cordon.yaml).

How to discover markers: [Extension — Identifying a disruption signal](extension.md#identifying-a-disruption-signal).
