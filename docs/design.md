# Design notes

Operator docs: [Overview](overview.md), [Configure](configure.md), [How-to](howto.md). This page is the architecture rationale for contributors.

## Goals

1. **Proactive scale-up** before predicted eviction  
2. **Signal-accelerated** detection (Karpenter / cloud / cordon)  
3. **Self-healing scale-back** without fighting HPA  
4. **Safe under load** — never force scale-down when HPA is raising  

## Non-goals

- Not an app-PDB replacement  
- Not a general scheduler  
- Not unpredictable failure (node crash, partition)  

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
| Window reconciler | `SpareReady`, cooldown, HPA-aware restore |
| Eviction webhook | Hard gate for voluntary Eviction |

Signals **buy Ready time**. The webhook **sequences** drain. `protected` is membership only.

Empty `disruptionSignals` defaults include Karpenter markers, `OutOfService`, and `NodeCordoned`. See [signals](signals.md).

## Capacity target (shipped: buffer)

```
target = current_replicas + spare
spare  = min(configured spare, maxBuffer)
```

Other strategies (request-capacity, pure HPA floor) were considered; buffer is the default.

**Anti-thrash:** do not scale back while HPA `desired`/`current` has moved *past* the spare Eviction Guard added.

## Scaling backends

`EvictionGuardPolicy.spec.backends` is a required named catalog. Protected Deployments must set `eviction-guard.io/scale-backend` to list catalog keys (optional `key=name`). There is no default bind. Token order is patch order; the first key is the SpareReady primary. Example policies define `deployment` and `hpa` so patching is data-driven (generic CR patcher).

Fan-out records each object on `EvictionGuardWindow.spec.actions` with its own baseline. Optional stamp annotations are separate from capacity patches.

Design follow-ups: [Scale targets](design-scale-targets.md).

## Window lifecycle

| Condition | Behavior |
|---|---|
| At-risk pods on vulnerable nodes | Stay Open; webhook gates Eviction |
| Spare not Ready off those nodes | Stay Open |
| At-risk gone + spare Ready | Scale back; arm `windowUntil`; Cooling |
| New at-risk during Cooling | Abort cooldown; reopen |
| `maxWindow` exceeded | `ForcedCool`; webhook allows; scale back; tombstone until nodes clear |
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

- StatefulSet / PVC-topology constrained scaling  
- Treating price-driven consolidation identically to capacity-preserving disruption without operator policy  

Status: shipped in `cmd/main.go`, `internal/controller`, `internal/webhook`, `pkg/evictgate`, Helm + Kustomize.
