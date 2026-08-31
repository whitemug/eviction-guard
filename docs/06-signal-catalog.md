# Disruption signal catalog

Eviction Guard has two layers:

1. **Built-in names** in `spec.disruptionSignals` (compiled detectors).
2. **Recipes** in `spec.customSignals` (taint / annotation / label matchers). Add these without a new release.

A marker belongs here only if it is on the Node, appears *before* drain, means impending eviction, and clears afterwards. How to confirm: `kubectl get node <n> -o yaml` before vs during a rotation.

## Built-in (`disruptionSignals`)

| Name | Marker | Default? |
|---|---|---|
| `KarpenterDisrupted` | Taint `karpenter.sh/disrupted` | yes |
| `KarpenterDeleteRequested` | Annotation `karpenter.sh/delete-requested-at` | yes |
| `OutOfService` | Taint `node.kubernetes.io/out-of-service` | yes |
| `SpotInterrupted` | Spot annotation / `spotinst.com/volatile` / out-of-service | no |
| `NodeCordoned` | `spec.unschedulable: true` | **no** (cordon ≠ drain) |

```yaml
spec:
  disruptionSignals:
    - KarpenterDisrupted
    - KarpenterDeleteRequested
    - OutOfService
    - NodeCordoned   # opt-in
```

## GKE (`customSignals`)

| Recipe name | Kind | Key | Notes |
|---|---|---|---|
| `GKEImpendingTermination` | taint | `cloud.google.com/impending-node-termination` | Host maintenance / GPU-TPU drain |
| `GKEMaintenanceOngoing` | label | `cloud.google.com/active-node-maintenance=ONGOING` | Workloads being stopped |
| `GKEMaintenanceWindow` | taint | `cloud.google.com/maintenance-window-started` | Scheduled maintenance window |

Standard node-pool **upgrades** often only cordon+drain. Use `NodeCordoned` if you accept false positives, or wait for one of the maintenance taints.

## AKS (`customSignals`)

| Recipe name | Kind | Key | Notes |
|---|---|---|---|
| `AKSUpgradeQuarantined` | label | `kubernetes.azure.com/upgrade-status=Quarantined` | Undrainable node during rolling upgrade |
| `AKSSpotEviction` | taint | `kubernetes.azure.com/scalesetpriority` is identity, **not** a signal | Do not use as a disruption signal |

AKS rolling upgrades cordon then drain. Prefer `NodeCordoned` on the upgrade node pool (`nodeFilter.labelSelector` for the pool) rather than treating every cordon in the cluster.

## Cluster Autoscaler (GKE / EKS / AKS)

| Recipe name | Kind | Key | Notes |
|---|---|---|---|
| `ClusterAutoscalerToBeDeleted` | taint | `ToBeDeletedByClusterAutoscaler` | Scale-down decided (NoSchedule) |
| `ClusterAutoscalerCandidate` | taint | `DeletionCandidateOfClusterAutoscaler` | Soft; CA may still back off. Use only if you want earlier notice |

## EKS + Karpenter

Prefer built-ins (`KarpenterDisrupted`, `KarpenterDeleteRequested`). AWS NTH typically cordons; combine `OutOfService` and optional `NodeCordoned`. Spot rebalance is often NTH-only — add whatever taint/annotation NTH writes in your cluster via `customSignals`.

Copy-paste policy: [`examples/policy-custom-signals.yaml`](../examples/policy-custom-signals.yaml), [`examples/policy-cordon.yaml`](../examples/policy-cordon.yaml).
