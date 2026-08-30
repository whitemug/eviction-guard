# Problem Statement: Throughput Dip During Node Disruption

## Background

In Kubernetes clusters running with a dynamic node provisioner such as **Karpenter** (or Cluster Autoscaler), nodes are intentionally rotated on a regular basis. This rotation happens for a variety of reasons:

- **`expireAfter`** — nodes are force-expired after a configured lifetime to enforce AMI drift rotation and node hygiene.
- **`consolidationPolicy`** — underutilized nodes are replaced with smaller / cheaper instances.
- **Drift detection** — nodes that drift from their declared `NodeClass` (AMI, userdata, security groups) are replaced.
- **External cloud events** — Spot interruption notices, ASG scale-in, and region-level maintenance windows.

## The Failure Mode

When a node is disrupted, Kubernetes evicts the pods running on it. The community standard for protecting workloads is the **Pod Disruption Budget (PDB)**.

PDBs are effective at guaranteeing a *minimum* number of replicas survive an eviction event. **But they do not prevent a transient capacity dip:**

1. The evicted pod is terminated.
2. The replacement pod is scheduled on another (possibly newly provisioned) node.
3. The replacement pod takes time to become `Ready` (image pull, startup, readiness probe).

> **Under sustained load, even a single eviction causes a throughput drop.** If one pod from a set of N is killed, the remaining N-1 replicas must absorb the full request volume. If those replicas are already near their resource limits (CPU/memory), request latency spikes and request success rate drops — the application's **overall throughput** degrades exactly when the cluster is under load.

PDB alone cannot fix this, because:

- PDB guarantees a floor (`minAvailable`) — it does not **grow capacity**.
- PDB does not force the cluster to have a hot, Ready replacement ready *before* the eviction lands.
- Under load, having exactly `minAvailable` replicas is not enough — the workload needs *spare* capacity.

## Why This Matters Most Under Load

The problem is self-reinforcing:

```
High load ──► high resource usage per replica
      │
      ├──► replicas already near their CPU/memory limits
      │
      ▼
Node rotation evicts one pod ──► remaining replicas take on more load
      │
      ├──► they push closer to limits ──► latency spikes
      │
      └──► replacement pod is cold ──► less effective immediately
```

The result: **a throughput degradation window** that starts at eviction and ends only when the replacement pod is fully Ready and warm.

## The Gap

The scheduling of new capacity is **reactive**. The cluster waits for:

1. The eviction to actually happen,
2. The pod to report `Pending/PodSchedulingFailed`,
3. The node provisioner to scale out,
4. The new pod to become Ready.

There is no mechanism that uses the *predictability* of node disruption to get ahead of it.

---

**This document defines a solution that closes that gap by proactively growing capacity ahead of predicted node evictions.**
