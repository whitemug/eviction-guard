# Eviction Guard

Close the capacity gap during Kubernetes node disruption. Detect *predictable* node evictions **before** they land, preemptively scale the affected Deployments up so warm replacement capacity is Ready in time — then automatically return replicas to baseline once the disruption passes.

## The Problem

Pod Disruption Budgets (PDBs) guarantee a *minimum* number of replicas survive an eviction, but they don't create **spare** capacity. When Karpenter rotates a node (`expireAfter`, drift, consolidation) or a cloud provider reclaims an instance (Spot interruption, ASG scale-in), the evicted pod is replaced **reactively** — the cluster waits, then scales, then the new pod pulls images and becomes Ready. Under sustained load, even one eviction causes a throughput dip while the remaining replicas absorb the load.

## The Idea

Node disruption is often **predictable**. Karpenter knows when it will expire/consolidate a node; cloud providers emit termination notices ahead of reclaim. Eviction Guard exploits that predictability to convert a reactive scale-up (cold-start cost) into a **proactive scale-up** (warm spares exist before the eviction lands), then self-heals back to baseline.

## Repository Layout

```
docs/
  01-problem.md          # Failure mode and why it matters under load
  02-solution-design.md  # Full architecture: signals, replica math, scale-back, controller vs CronJob
  03-mvp-quickstart.md   # Running the CronJob MVP against a test cluster
```

## Key Mechanics

- **Signal-based (G2):** reacts to Karpenter disruption markers and cloud termination notices — not random polling.
- **Safe under load (G4):** grows capacity; never shrinks a genuinely saturated workload.
- **Self-healing (G3):** HPA-aware scale-back with a disruption window + cooldown so it never fights the HPA or Karpenter consolidation.
- **Extensible action (§7):** the scale step is a pluggable backend — `deployment` (patch `.replicas`), `hpa-min` (raise HPA floor), or `crd` (increment a count on your own CR) — so Eviction Guard composes with whatever defines "capacity" in your stack.
- **Grounded in prior art (§11):** compared against Karpenter disruption pre-warming, `do-not-disrupt`, Cast AI's Rebalancer, AWS NTH, Koordinator, and `cluster-overprovisioner` — the gap they all leave (they warm *nodes*, not *pods*).

## Status

Design draft — MVP (CronJob) specified; production Controller extension outlined. See `docs/02-solution-design.md` §9–§10.

## License

MIT — see [LICENSE](LICENSE).
