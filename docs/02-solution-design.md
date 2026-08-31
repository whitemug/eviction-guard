# Solution Design: Eviction Guard

> Proactive node-disruption protection: grow workload capacity *ahead* of predicted pod evictions, then let it settle back to normal once the disruption has passed.

## 1. Overview

**Eviction Guard** is a mechanism that detects *predictable* node disruptions before they evict pods, and preemptively scales up the affected Deployments so that warm, Ready replacement capacity exists *before* the old pods are terminated.

The core insight: **node disruption is often predictable.** Karpenter knows *when* it will expire or consolidate a node; cloud providers emit termination notices before reclaiming instances. Eviction Guard exploits this predictability to convert a *reactive* scale-up (which has a cold-start delay) into a *proactive* scale-up (which overlaps with the pre-disruption window).

Eviction Guard is **complementary** to PDB, not a replacement:

| Mechanism | Guarantees | Gap |
|---|---|---|
| **PDB** | At least `minAvailable` replicas survive | No spare capacity; cold replacement |
| **HPA** | Every pod is scaled on observed metrics | Reacts *after* load is already spiking |
| **Eviction Guard** | Warm spare replicas exist *before* eviction | Needs a predictable disruption signal |

Throughout this document, **Eviction Guard** is abbreviated **EVG**.

## 2. Design Goals & Non-Goals

### Goals
1. **G1 — Proactive scale-up:** increase replicas before predicted eviction, not after.
2. **G2 — Signal-based:** react to predictable disruption signals from Karpenter *and* cloud provider termination notices.
3. **G3 — Self-healing (scale-back):** return replicas to baseline once the disruption window has passed, without operator intervention and without fighting HPA or Karpenter consolidation.
4. **G4 — Safe under load:** never scale *down* a workload that is genuinely saturated.

### Non-Goals
- **NG1 — Not a PDB replacement.** Eviction Guard does not manage disruption budgets.
- **NG2 — Not a general scheduler.** Eviction Guard does not place pods; it only grows/shrinks Deployment replicas.
- **NG3 — Not handling *unpredictable* failures** (node crash, network partition). Eviction Guard is strictly for *announced/predictable* disruption.

## 3. Architecture

```
                        ┌────────────────────────────────────────────┐
                        │            Disruption Signals              │
                        │                                            │
   Karpenter ──► taints/ ─┐                                          │
   (expireAfter, drift,   │  annotate vulnerable node               │
    consolidation)        │                                          │
                        ┌─▼────────────────────────────────────────┐ │
                        │     EVG controllers                      │ │
                        │                                          │ │
   Cloud notices ──►    │  1. Enumerate vulnerable nodes           │ │
   (Spot interrupt,     │  2. Map vulnerable node ──► Deployments  │ │
    ASG scale-in,       │  3. Compute target capacity delta        │ │
    maintenance)        │  4. Apply via scaling backend (§7)       │ │
                        │  5. Record a disruption "window"         │ │
                        │  6. After window: scale back             │ │
                        └───────────────┬──────────────────────────┘ │
                                        │  backend: patch replicas / ─┘
                                        │  minReplicas / CR count
                                        ▼
                              ┌────────────────────┐
                              │   Target workload  │
                              └────────────────────┘
```

### 3.1 Control plane

The shipping implementation is two `controller-runtime` reconcilers (Policy + Window) that watch Nodes and persist state on `EvictionGuardWindow`. `EvictionGuardPolicy.spec.nodeFilter` is how operators and other plugins restrict which nodes are watched. `spec.customSignals` is how they add newly discovered cloud markers without a rebuild.

A CronJob was considered for an early MVP and is **not** shipped: Spot-class windows (~2 min) and reliable scale-back state need watches and a CRD, not a poll.

## 4. Detecting Vulnerable Nodes (G2)

Two independent signal sources are combined. A node is treated as **"vulnerable"** if *any* of the following indicate an imminent eviction:

### 4.1 Karpenter signals
Karpenter writes signals on the node object that the Policy controller watches:
- **`karpenter.sh/do-not-disrupt: "true"`** (annotation) — present during an ongoing disruption; a node about to be disrupted.
- **Drift/expiration timestamps** — Karpenter schedules disruption; the node may carry the `karpenter.sh/disruption` label / `karpenter.sh/delete-requested-at` timestamp.
- Node `Taints` — Karpenter adds `karpenter.sh/disrupted` (PreferNoSchedule / NoSchedule) when it begins draining a node.

> **Sizing note for Karpenter `disruptionBudget`:** since Karpenter v1.14, a `disruptionBudget` bound on how many nodes can be disrupted simultaneously is available. Eviction Guard works *alongside* this — `disruptionBudget` limits concurrency of eviction; Eviction Guard ensures the *survivors* plus *spares* are enough to absorb load. Set `disruptionBudget: WhenEmptyOrUnderutilized` with a reasonable budget while Eviction Guard is active.

### 4.2 Cloud provider termination notices
> These come in two cadences (see §10.5) — **short notice** (~2 min, Spot interruption) favors a Controller; **long notice** (10–20 min, e.g. ASG Rebalance Recommendation) fits either.

- **AWS Spot Interruption** → `aws-node-termination-handler` or the SQS-based interruption queue marks the node `spotinst.com/volatile` / adds the `node.kubernetes.io/out-of-service` taint; ~2-minute notice.
- **AWS Spot/AZ Rebalance Recommendation** → 10–20 min early warning, typically surfaced via NTH (Karpenter delegates rebalance handling to NTH).
- **AWS ASG scale-in** → lifecycle hook / termination notice.
- **GCE/GKE** → maintenance events / instance `maintenanceEvent` can be surfaced.
- **Azure AKS** → `node.kubernetes.io/out-of-service` taint on a node that will be evicted (the Karpenter Azure provider uses this).

### 4.3 Consolidation
A node is **vulnerable** when classified as such by any source. The classification logic:

```
isVulnerable(node) =
      hasTaint(node, "karpenter.sh/disrupted")
   || hasTaint(node, "node.kubernetes.io/out-of-service")
   || annotation(node, "karpenter.sh/delete-requested-at") != ""
   || isSpotInterrupted(node)
   || hasMaintenanceNotice(node)
```

## 5. Mapping Vulnerable Node → Affected Workloads (Step 2)

Once a node is vulnerable, determine which Deployments run pods on it. Eviction Guard selects Deployments that satisfy **all** of:

1. **Pod is scheduled on the vulnerable node** (via `spec.nodeName` of pods owned by the Deployment, or via node `ownerReferences`/pod anti-affinity).
2. **Deployment has a PDB** — only protect workloads that declare a minimum availability (respected SIG; ensures we're not scaling everything).
3. **Deployment is opted-in** to Eviction Guard via a label, e.g.:
   ```
   eviction-guard.io/enabled: "true"
   ```
   This prevents Eviction Guard from scaling arbitrary system workloads.

> Optionally, restrict to Deployments whose pods have a **readiness twice-gated** (startup + readiness) so spares are counted only when truly Ready.

## 6. Computing the Target Replica Count (Step 3)

The number to scale to must be **safe under load** (G4) and **enough to cover the gap** (G1). There are three strategies:

### Strategy A — Buffer-based (recommended default)
```
target = current_replicas + spare
```
where `spare` = minimum of:
- `ceiling(min_count / minAvailable × disruption_rate)`, and
- a configurable `maxBuffer` bound.

> Simple, predictable, and expresses "leave room for one node's worth of pods."

### Strategy B — Request-capacity based
Compute how much allocatable CPU/memory the vulnerable nodes hold, and ensure total cluster *spare allocatable* ≥ that amount after scale-up:
```
addedCap = Σ allocatable(nodes being drained)
target = smallest N such that spareAllocatable ≥ addedCap
```
This is more accurate for heterogeneous instance types but requires resource accounting.

### Strategy C — HPA-aware (minimal)
Ask the HPA's current `status.currentReplicas` and set a **floor** equal to current + 1 per vulnerable node, letting the HPA do the rest. Least control, lowest complexity.

> **Anti-thrashing rule:** Never scale down while load is high. Eviction Guard records a `window` (Section 8) and will not scale back if the Deployment's HPA status shows `currentReplicas >= targetReplicas` (i.e., it's already scaling up on its own).

## 7. Extensible Scaling Backend (the "Action")

> **Design principle:** Eviction Guard does **not** assume a single way to add capacity. "Increasing capacity" is an abstract operation that is realized through a pluggable **scaling backend** — chosen per workload. This keeps Eviction Guard useful regardless of what the rest of your stack interprets as "more replicas."

### 7.1 The abstraction

Every workload is annotated with a *backend* and an *object key*. Eviction Guard computes only the **capacity delta** (Section 6) and hands it to the selected backend:

```
target_capacity = current_capacity + spare      (Section 6)
            │
            ▼
    [ Scaling Backend : ScaleUp(delta) ]
            │
      ┌─────┼──────────┬─────────────┐
      ▼     ▼          ▼             ▼
  Deployment  HPA-on-   CRD count     ... (custom)
  .replicas   .minReplicas
```

```
eviction-guard.io/scale-backend: deployment | hpa-min | crd | custom
                                 (comma-separated list: deployment,hpa-min,crd)
eviction-guard.io/hpa-target:    <namespace/name of the HPA>
eviction-guard.io/scale-target:  <CR: group/version/namespaces/ns/kind/name>
```

### 7.2 Backend: `deployment` (default)

Directly patch `Deployment.spec.replicas`.

```
patch Deployment.apps/<name>  {"spec": {"replicas": target}}
```

- Pros: simple, no HPA required, works everywhere.
- Scale-back: restore `spec.replicas` to baseline (respecting HPA desired, §8).

### 7.3 Backend: `hpa-min`

Raise the **HPA floor** so the Horizontal Pod Autoscaler grows the workload, rather than Eviction Guard pinning a fixed number.

```
patch HorizontalPodAutoscaler/<name>  {"spec": {"minReplicas": target}}
```

Behavior on scale-up:
- Set `minReplicas` to the target. HPA then converges `currentReplicas` toward the (raised) floor; any spare above steady-state is governed by HPA, not Eviction Guard.

Behavior on scale-back:
- Restore `minReplicas` to the original baseline. **Critical safety:** only lower `minReplicas` back down when the HPA's `status.currentReplicas` is at or below the target, otherwise the HPA would immediately scale back up (thrash). Eviction Guard never sets `minReplicas` below the HPA's `status.currentReplicas` at scale-down time.

> **Trade-off vs `deployment`:** `hpa-min` is more robust to sustained load (HPA keeps real-time control of the upper bound), but it only *encourages* scale-up — if the HPA has no metric pressure, it may never add the spare. Combine with a small HPA tolerance or use `deployment` when you need a guaranteed count.

### 7.4 Backend: `crd` (custom resource count)

Increment a counter field on a CRD that **your operator/controller** interprets as a scale intent — e.g. a `replicas`/`spareCapacity` field on a custom object.

```
patch <CustomResource>/<name>  {"spec": {"replicas": <current+spare>}}
```

Behavior:
- Eviction Guard writes the *desired total* into the CR's `spec`.
- Optionally stamps visibility annotations on every scaled object (`spec.stamp` / `eviction-guard.io/stamp`): baseline, scaled-to, and active at scale-up; window-until only when cooldown starts. Keys are configurable. Capacity backends never write these — stamps are a separate annotation patch.
- Your operator watches the CR and reconciles actual capacity (Deployment, StatefulSet, Knative Revision, VirtualService weight, etc.).
- Scale-back: Eviction Guard restores the CR field to baseline and deletes stamp annotations; your operator does the actual drain.

> **Why useful:** decouples Eviction Guard from your scaling topology entirely. Ideal when capacity is defined by higher-level abstractions (serverless/Knative, a custom autoscaler, a service-mesh weighted backend) rather than a plain Deployment.

### 7.5 Backend selection rules

- Default: `deployment`.
- If the workload is HPA-managed and you want HPA to keep controlling the ceiling: `hpa-min`.
- If you own an operator/CRO owning replication: `crd`.
- **Fan-out:** a workload may list more than one backend. `eviction-guard.io/scale-backend: deployment,hpa-min,crd` (or policy `additionalBackends`) patches all of them to the same desired count. Each object keeps its own baseline on `EvictionGuardWindow.spec.actions`. Scale-up order is deployment → hpa-min → crd; scale-down is hpa-min → deployment → crd so the HPA floor cannot fight replica restore. Use `eviction-guard.io/hpa-target` for the HPA and `eviction-guard.io/scale-target` for the CR.
- Each backend MUST implement both `ScaleUp(desired)` and `ScaleDown(baseline)` so the window lifecycle (§8) is symmetric. Backends change capacity only; stamps are applied afterward.

### 7.6 Recording which backend was used

Persisted `EvictionGuardWindow` records store every action so scale-back uses the identical path:

```json
{"kind":"Deployment","ref":"app/web","backend":"deployment",
 "baseline":4,"scaledTo":6,
 "actions":[
   {"backend":"deployment","kind":"Deployment","name":"web","baseline":4,"scaledTo":6},
   {"backend":"hpa-min","kind":"HorizontalPodAutoscaler","name":"web","baseline":2,"scaledTo":6}
 ]}
```

## 8. Scale-Back (G3)

The hardest part. Eviction Guard must return replicas to baseline **without**:
- Fighting the HPA,
- Causing another throughput dip,
- Or leaving the cluster permanently over-provisioned (which would fight Karpenter's own consolidation).

### Design: a cleared "disruption window" + cooldown
1. When the last vulnerable node clears **and** `status.spareReady` is true (enough Ready pods off those nodes), set `EvictionGuardWindow.spec.windowUntil` to `now + scaleBackAfter` (e.g. 15m). The field is empty while the window is Open.
2. On the next reconcile **after** `windowUntil`, verify:
   - No vulnerable nodes remain for this workload.
   - The Deployment has not been scaled up independently by HPA since (compare `status.currentReplicas`).
   - The Deployment's request success / error rate (if metrics available) is healthy.
3. If all pass, issue a scale-down toward baseline — but **one node/window at a time** and only down to the **baseline + HPA desired**, never below what HPA wants.

> **Why not below HPA desired:** if HPA is legitimately maintaining N replicas because of sustained load, Eviction Guard must not fight it. Eviction Guard only removes the *spare* above the HPA's desired count, and only after load has subsided.

### Scale-back policy matrix

| Condition | Action |
|---|---|
| No vulnerable nodes, cooldown elapsed, load healthy | Scale down toward baseline |
| No vulnerable nodes, cooldown elapsed, load still high (HPA scaling up) | Hold; Eviction Guard's spare remains |
| Vulnerable nodes still present | Wait (no scale-down) |
| Nodes clear, spare not Ready off those nodes | Wait (Open; cooldown not started) |
| New vulnerable node appears during cooldown | Reset cooldown |

## 9. Implementation

The shipping code is two reconcilers in `internal/controller`, started from `cmd/main.go`:

- **Policy controller** — watches Nodes and Pods, applies `nodeFilter`, scales opted-in workloads, opens an `EvictionGuardWindow`.
- **Window controller** — waits for `SpareReady` (Ready pods off vulnerable nodes), then cooldown, HPA-aware scale-back, closes the window.

State lives on the `EvictionGuardWindow` CRD (not a ConfigMap). Install with Helm or Kustomize (`docs/03-install.md`). Add newly discovered cloud markers with `spec.customSignals` (`docs/04-extension.md`).

## 10. Production notes

- Leader election (on by default)
- Kubernetes Events on scale-up / scale-back / SpareReady
- Metrics `evg_matched_nodes`, `evg_vulnerable_nodes`, `evg_current_spare`, `evg_scale_actions_total`, `evg_spare_not_ready`
- Extra RBAC (`extraClusterRoleRules`) when using the `crd` backend
- Validating webhook (Helm default) for workload annotations and policy `namePattern`
- `spec.maxConcurrentWindows` (default 8, `0` = unlimited) so one disruption wave cannot scale every opted-in Deployment at once; waiters get a slot after a window closes
- `spec.maxWindow` (default 2h, `0` = unlimited) force-cools a window that stays Open too long (stuck taint / spare never Ready), then scales back and will not reopen until those nodes clear
- Policy reconcile is enqueued for a **node** only when that node matches a policy `nodeFilter` (updates union old and new, so a node leaving a filter still reconciles)
- Policy reconcile is enqueued for a pod only when that pod's node matches the policy `nodeFilter` and is currently vulnerable, and only when the pod is bound, relabeled, or deleted (Ready/status flips stay on the window controller)

## 11. Related Work & Design Inspiration

Eviction Guard is not a completely novel idea — several existing systems tackle parts of the "don't disrupt throughput" problem. Studying how they went about it shaped the design above and, importantly, **defines the precise gap Eviction Guard fills** (they all warm up *nodes*; none warm up *pods*).

### 11.1 Karpenter disruption pre-warms replacement **nodes** (not pods)

Karpenter's disruption flow is already proactive at the *capacity* layer:

1. Identify disruptable nodes.
2. Run a **scheduling simulation** with the pods on the node to determine if replacement nodes are needed.
3. Taint the node (`karpenter.sh/disrupted:NoSchedule`).
4. **Pre-spin any replacement nodes and wait for them to become Ready** *before* terminating the source.
5. Terminate and drain the source node.

> **Inspiration / gap:** Karpenter guarantees the *node* is ready, but **not the pods on it**. A replacement pod still needs image pull, container start, and readiness — the exact cold window Eviction Guard targets. Eviction Guard is the *pod-level* complement to Karpenter's node-level pre-warming. Notably, this also means **Eviction Guard's spares have nodes to land on** during the window, so it composes cleanly with Karpenter.

### 11.2 Karpenter `karpenter.sh/do-not-disrupt` annotation

Karpenter's native escape hatch. Supports a **boolean** (permanent) or a **duration** (`30m` — protect for a window after the pod starts).

> **Inspiration:** confirms the community's mental model of *time-boxed disruption windows*. Eviction Guard's "scale-back cooldown" is the same pattern applied to replicas instead of individual pods.

### 11.3 Karpenter issue #1599 — the exact gap, upstream

The community explicitly reports this problem on Karpenter itself:

> "Current behaviour breaks zero downtime on the pod moving between nodes. Karpenter should wait until new pods are marked as healthy before destroying old ones."

And a maintainer confirmation: `maxUnavailable: 0` / `maxSurge: 1` **does not work** for evictions — the Kubernetes eviction API deletes before it creates. This is the strongest validation that a PDB-based answer is insufficient and an external mechanism like Eviction Guard is warranted.

### 11.4 Cast AI "Continuous Rebalancer" (commercial) — closest analogue

Cast AI ships a workload-aware rebalancer that "provisions replacement capacity **before draining source nodes**. This eliminates the scheduling gap between eviction and rescheduling that causes latency spikes in tight clusters with high replica counts."

> **Takeaway:** Eviction Guard's premise is commercially validated. The differentiating angle for an open, docs-first design is: event-triggered (not always-on), Deployment-replica-focused, HPA-aware scale-back, and provider-agnostic (not just AWS EKS).

### 11.5 AWS Node Termination Handler (NTH)

NTH watches EC2/ASG termination signals (Spot interruption, maintenance, ASG scale-in, AZ rebalance) and **cordon + drain**s before the instance dies. It is a *signal source + eviction* mechanism, not a scale-out mechanism. In Karpenter, Spot **Rebalance Recommendations** (10–20 min early warning) are delegated to NTH because Karpenter doesn't handle them natively.

> **Inspiration for signals (Section 4):** NTH confirms the *two-mode* signal architecture — short notice (Spot interruption, ~2 min) vs. long notice (Rebalance recommendation, 10–20 min). Short notices are why Eviction Guard is a controller, not a poller.

### 11.6 Koordinator `koord-descheduler` CustomPriority

Koordinator proactively evicts pods off a node *pool* onto another pool (e.g. spot → on-demand) for cost/consolidation, with a `DrainNode` mode and optional `autoCordon`.

> **Relevance:** An inverse (but interoperable) mechanism. Where Koordinator *moves* workloads off, Eviction Guard *grows capacity* ahead of the move. Its `autoCordon`-then-drain sequencing and its per-pool priority tiers are a clean reference for Eviction Guard's "which nodes are at risk" determination.

### 11.7 Cluster Overprovisioning (`cluster-overprovisioner`) / pre-warmed autoscaling

The classic trick: run **dummy buffer pods** (with a creator annotation) on-runaway priority class so spare nodes stay warm, and size the buffer via a proportional autoscaler.

> **Inspiration:** A *static/continuous* buffer differs fundamentally from Eviction Guard's *event-triggered* buffer. Overprovisioning pays for idle capacity *all the time*; Eviction Guard pays only around a disruption window. Eviction Guard is the on-demand, lower-cost alternative — but overprovisioning remains a valid simple fallback for clusters too small to justify Eviction Guard.

### 11.8 Synthesized design borrowings

| Idea | Borrowed from | How Eviction Guard adapts it |
|---|---|---|
| Pre-warm replacement *before* terminating source | Karpenter disruption / Cast AI Rebalancer | Pre-scale replicas before eviction |
| Time-boxed protection | `do-not-disrupt: 30m` | Cooldown + window-based scale-back |
| Eviction API deletes-before-creates | Karpenter #1599 | Justifies growing replicas, not just rebudgeting |
| Two signal cadences (short/long notice) | NTH, Karpenter Rebalance | Map urgency; short windows need a controller |
| Buffer capacity on demand | `cluster-overprovisioner` | Event-triggered spare, not always-on |

## 12. Operational Considerations

- **Throttling / quiet periods:** avoid scaling during planned deployments independently managed by operators.
- **Interop with HPA:** Eviction Guard only *raises* a floor and scales back to HPA's desired; it never acts as the sole scaling authority.
- **Interop with Karpenter consolidation:** Eviction Guard's spares increase utilization briefly; Karpenter's `consolidationPolicy: WhenEmptyOrUnderutilized` will naturally reclaim the spare nodes after window close — that is desirable and expected.
- **Idempotency:** scaling to the same target is a no-op; window records are keyed by `namespace/name`.
- **Metrics & alerts:** `evg_current_spare`, `evg_vulnerable_nodes`, `evg_matched_nodes`, `evg_scale_actions_total`.

## 13. Acceptance criteria

In a test cluster under synthetic load:

1. A Karpenter `expireAfter` rotation on a node running an opted-in Deployment **does not** cause a measurable throughput/error-rate regression.
2. Eviction Guard scales the Deployment up *before* the eviction lands (replacement pods Ready before old pod terminates).
3. After rotation, replicas return to baseline automatically (cooldown + HPA-aware scale-back).
4. A Deployment **not** opted-in shows no change (isolation confirmed).

## 14. Out of scope for v1alpha1

- PVC-backed / StatefulSet workloads where scaling is constrained by storage topology
- Treating Karpenter **consolidation** (price-driven) the same as capacity-preserving disruption
- A built-in NTH/cordon signal — configure it with `spec.customSignals` until it is widely used

---

*Status: v1alpha1 controller shipped (`cmd/main.go`, `internal/controller`, Helm + Kustomize).*
