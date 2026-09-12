# Design notes

Operator docs: [Overview](overview.md), [Configure](configure.md), [How-to](howto.md). This page is the architecture rationale for contributors.

## Goals

1. **Proactive scale-up** before predicted eviction  
2. **Signal-accelerated** detection (Karpenter / cloud / cordon)  
3. **Self-healing scale-back** — revert what we raised (baseline restore)  
4. **Scaler-aware binding** — prefer HPA/KEDA floors over `Deployment.replicas` when a scaler owns capacity  

## Non-goals

- Not an app-PDB replacement  
- Not a general scheduler  
- Not unpredictable failure (node crash, partition)  
- Not inferring “don't restore” from live replica count (use `skipDownscaling`)  

## Control plane

```
   Karpenter / cloud / cordon
            │  node markers (early)
            ▼
   Policy controller ──► scale backends ──► EvictionGuardWindow
            │
            ▼
   drain uses pods/eviction
            │
            ▼
   Validating webhook (pkg/evictgate)
      deny until SpareReady
      then one at-risk pod at a time
            │
            ▼
   Window controller ──► scale back ──► Cooling ──► close
```

| Piece | Role |
|---|---|
| Policy reconciler | `nodeFilter` + signals → open window + scale |
| Window reconciler | `SpareReady`, cooldown, baseline restore (`skipDownscaling` optional) |
| Eviction webhook | Hard gate for voluntary Eviction |

Signals **buy Ready time**. The webhook **sequences** drain. `protected` is membership only.

Empty `disruptionSignals` defaults include Karpenter markers, `OutOfService`, and `NodeCordoned`. See [signals](signals.md).

## Capacity target (shipped: buffer)

```
target = current_replicas + spare
spare  = min(configured spare, maxBuffer)
```

Other strategies (request-capacity, pure HPA floor) were considered; buffer is the default.

**Restore:** each Window action reverts to its recorded baseline unless the catalog entry set `skipDownscaling` (up-only). Prefer targeting scaler floors so the autoscaler owns desired count after the window.

## Scaling backends

`EvictionGuardPolicy.spec.backends` is a required named catalog. Protected Deployments must set `eviction-guard.io/scale-backend` to list catalog keys (optional `key=name`). There is no default bind. Token order is patch order; the first key is the SpareReady primary. Example policies define `deployment` and `hpa` so patching is data-driven (generic CR patcher).

When an HPA or KEDA ScaledObject owns the workload, bind **that** backend (raise `minReplicas` / `minReplicaCount`). Bind `Deployment.replicas` only when no scaler is present — or set `skipDownscaling: true` on Deployment if you still raise it for spare and do not want EVG to yank replicas back.

Fan-out records each object on `EvictionGuardWindow.spec.actions` with its own baseline. Optional stamp annotations are separate from capacity patches.

Design follow-ups: [Scale targets](design-scale-targets.md) (catalog locked for 0.2.0).

## Window lifecycle

| Condition | Behavior |
|---|---|
| At-risk pods on vulnerable nodes | Stay Open; webhook gates Eviction |
| Spare not Ready off those nodes | Stay Open |
| At-risk gone + spare Ready | Scale back; arm `windowUntil`; Cooling |
| New at-risk during Cooling | Abort cooldown; reopen |
| `maxWindow` exceeded | `ForcedCool`; webhook allows; scale back; tombstone until nodes clear |
| Capacity patch rejected (admission/RBAC/…) | `CapacityApplied=False`; keep Window open; wait for spare or `maxWindow` fail-open |
| Cooling elapsed | Close / delete |

State is the `EvictionGuardWindow` CRD (not a ConfigMap).

## Why not a hold PDB

A standing PDB on opt-in labels blocked drains the controller never intended to handle (unknown signals, `nodeFilter` mismatch). The Eviction API webhook only denies when a matching policy sees disruption (or an active window), and always pairs deny with “controller should be scaling.” Direct pod delete still bypasses both PDB and this webhook — Kubernetes default.

## Community context (short)

- Karpenter waits for replacement **nodes**, not spare **replicas** — EVG fills that gap.  
- Deployment `maxUnavailable: 0` does not sequence Eviction (delete-before-create).  
- NTH / Spot show short (~2m) vs long (10–20m) notice cadences — watches beat polling.  

## Acceptance (v1alpha1)

1. Opted-in workload under load: Karpenter-style drain does not cause a clear throughput regression.  
2. Spare Ready before at-risk pod terminates (webhook deny until then).  
3. Automatic scale-back after cooldown.  
4. Non-opted-in Deployments unchanged.  

## Out of scope for v1alpha1

- StatefulSet / non-Deployment primary workloads — Target abstraction planned before API freeze (see **D2** below)  
- Treating price-driven consolidation identically to capacity-preserving disruption without operator policy  

Status: shipped in `cmd/main.go`, `internal/controller`, `internal/webhook`, `pkg/evictgate`, Helm + Kustomize.

## Architecture decisions (2026-09)

Accepted product law for the current design cut:

| ID | Decision |
|---|---|
| D1 | Cross-namespace `scale-backend` (`key=ns/name`) stays supported; isolation is Policy/RBAC/ops, not a code deny |
| D2 | Plan a Target abstraction (membership / SpareReady / gate beyond Deployment) before API freeze |
| D3 | On `CapacityApplied=False`, keep deny until SpareReady or `maxWindow` ForcedCool (retry transient API failures; no early fail-open) |
| D4 | Cluster-singleton install now; do not freeze APIs/RBAC in a way that blocks future namespace-scoped installs |
| D5 | Chart default manager `replicaCount: 2` (webhook HA), overridable |
| D6 | Multi-policy ownership stays lex-first-by-name + `policy-pin` (no specificity scoring) |
| D7 | External / empty-Actions windows: `maxWindow` remains the safety valve; no dedicated stuck signals for now |

Catalog / scale-backend rationale (locked for 0.2.0): [Scale targets](design-scale-targets.md).

### Maintainer backlog (next)

1. Target abstraction: inventory Deployment-hardcoded call sites; design membership / SpareReady / gate beyond Deployment before API freeze.  
2. Applied-vs-planned actions on partial multi-backend apply failure.  
3. Catalog `defaultWhenUnset` (or similar) instead of hard-coded `1`.  
4. Window indexing / watch fan-out; chart metrics NetworkPolicy.  
5. E2E: CapacityApplied → ForcedCool; multi-policy pin; cross-ns backend happy path.  
