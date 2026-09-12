# What Eviction Guard does

Eviction Guard reduces **throughput dips** when Kubernetes **voluntarily** drains nodes (Karpenter rotation, Spot interruption, pool upgrades, `kubectl drain`).

## The problem

App **PodDisruptionBudgets** keep a replica *floor*. They do **not** grow capacity before eviction. Under load, losing even one pod while a cold replacement starts causes latency and error spikes.

Node disruption is often **predictable** (taints, cordon, cloud notices). Eviction Guard uses that lead time.

## The product

For opted-in Deployments:

1. **Scale early** when a matching node shows a disruption signal (Karpenter taint, cordon, …).
2. **Hold voluntary eviction** via a validating webhook on `pods/eviction` until spare pods are **Ready off the dying node**.
3. **Allow one at-risk pod at a time**, then **scale back** after a cooldown (revert to baseline).

```
disruption on node  →  scale spare  →  Eviction denied until SpareReady
                                    →  allow next at-risk pod
                                    →  scale back → close window
```

`eviction-guard.io/protected: "true"` on the **pod template** is **opt-in only**. It does not block eviction by itself; the webhook does.

## Resources

| Resource | Role |
|---|---|
| `EvictionGuardPolicy` (cluster) | Which nodes, which signals, spare / cooldown settings |
| `EvictionGuardWindow` (namespace) | One disruption episode for one Deployment |
| Validating webhook | Annotation checks + **`pods/eviction` gate** |

## Compared to other mechanisms

| Mechanism | Guarantees | Gap |
|---|---|---|
| App PDB | Floor of replicas survives | No spare; cold replacement |
| HPA | Scale on metrics | Reacts *after* load spikes |
| Eviction Guard | Warm spare *before* eviction; webhook sequences drain | Node disruption / voluntary Eviction only |

## Non-goals

- Not a replacement for per-app PDBs  
- Not protection against node crash, OOM, or network partition  
- Not a general scheduler or placement engine  
- Direct `kubectl delete pod` bypasses the Eviction API (and this webhook) — that is normal Kubernetes  

Next: [Install](install.md) → [Configure](configure.md) → [How-to](howto.md).
