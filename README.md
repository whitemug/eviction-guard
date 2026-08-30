# Eviction Guard

Kubernetes controllers that detect **predictable** node disruption (Karpenter drain, Spot interruption, `out-of-service` taints) and **preemptively scale** opted-in workloads so replacement pods are Ready before the old ones die. After a cooldown, capacity returns to baseline without fighting the HPA.

Eviction Guard is complementary to Pod Disruption Budgets: PDBs keep a floor; this project adds a temporary spare.

## How it works

Two reconcilers:

1. **Policy controller** — watches Nodes. For each `EvictionGuardPolicy`, it applies that policy's `nodeFilter`, detects disruption signals, maps at-risk pods to opted-in Deployments, scales via a pluggable backend, and opens a `ProactiveWindow`.
2. **Window controller** — waits until extra pods are **Ready off the dying node** (`status.spareReady`), then after `scaleBackAfter` restores capacity (never below what HPA is already running).

```
Node taint / annotation change
        │
        ▼
EvictionGuardPolicy  ── nodeFilter ──► subset of nodes
        │
        ▼
opted-in Deployment + PDB
        │
        ▼
scale backend (deployment, hpa-min, and/or crd)
        │
        ▼
ProactiveWindow  ── cooldown ──► scale back
```

## Node filters

Policies are cluster-scoped. **`spec.nodeFilter` is how you (and other operators) choose a subset of nodes.** Empty filter = all nodes. Multiple policies can coexist (Spot vs on-demand, one node pool, one AZ).

```yaml
apiVersion: eviction-guard.io/v1alpha1
kind: EvictionGuardPolicy
metadata:
  name: spot-workers
spec:
  nodeFilter:
    capacityTypes: ["spot"]
    labelSelector:
      matchLabels:
        karpenter.sh/nodepool: default
    excludeLabelSelector:
      matchLabels:
        eviction-guard.io/ignore: "true"
    zones: ["us-east-1a"]
    namePattern: "ip-10-0-*"
  spareReplicas: 1
  maxBuffer: 4
  maxConcurrentWindows: 8   # 0 = unlimited; extra at-risk apps wait until a window closes
  maxWindow: 2h             # 0 = unlimited; force-cool if still Open this long
  defaultBackend: deployment
  # additionalBackends: [hpa-min]   # also raise HPA minReplicas on every opted-in workload
  scaleBackAfter: 15m
```

See `examples/` and the [signal catalog](docs/06-signal-catalog.md) for more.

## Workload opt-in

```yaml
metadata:
  labels:
    eviction-guard.io/enabled: "true"
  annotations:
    eviction-guard.io/scale-backend: deployment,hpa-min   # comma-separated; or a single backend
    eviction-guard.io/hpa-target: default/web             # optional; defaults to HPA named like this Deployment
    eviction-guard.io/scale-back-after: 20m               # optional; overrides policy scaleBackAfter
    eviction-guard.io/stamp: "true"                       # optional; annotate Deployment/HPA/CR with baseline/scaled-to
    # eviction-guard.io/scale-target: example.com/v1/namespaces/default/Widget/web
    # eviction-guard.io/crd-replicas-path: spec.replicas
```

A matching PodDisruptionBudget is required unless the policy sets `requirePDB: false`. Helm’s validating webhook rejects unknown `scale-backend` values, a bad `scale-target` / `scale-back-after`, and `crd` without a full GVK target.

## Install

Building from source requires **Go 1.27+**.

**Helm (recommended)**

Published chart (tagged releases, Cosign-signed):

```bash
helm install eviction-guard oci://ghcr.io/whitemug/charts/eviction-guard \
  --version 0.1.0 \
  --namespace eviction-guard-system --create-namespace
kubectl apply -f examples/policy-spot.yaml   # or your own EvictionGuardPolicy
```

From a clone of this repo: `helm install eviction-guard charts/eviction-guard -n eviction-guard-system --create-namespace`.

The chart does **not** create a policy by default (so a bare install does not watch the cluster). Enable one with `--set defaultPolicy.enabled=true` or apply `examples/`.

**Kustomize**

Requires cert-manager (webhook TLS).

```bash
make install          # CRDs
kubectl apply -k config/default
```

Build a local image first (`make docker-build IMG=...`) or wait for a published `ghcr.io/whitemug/eviction-guard` tag.

## Integrate from other Kubernetes plugins

Three layers, pick one:

| Layer | Who it's for | What you do |
|---|---|---|
| **CRDs** | Any operator / GitOps / Helm chart | Apply `EvictionGuardPolicy` with a `nodeFilter`. Watch `ProactiveWindow` to observe actions. |
| **Workload annotations** | App charts | Set `eviction-guard.io/enabled` and optional `scale-backend: deployment,hpa-min` (or `crd`) so immediate replicas, the HPA floor, and/or *your* CR are updated together. |
| **Go module** | Controllers compiled with this binary | `plugin.RegisterBackend` / `RegisterSignal` / `RegisterFilter`. |

Public packages:

- `github.com/whitemug/eviction-guard/api/v1alpha1` — types and well-known labels
- `github.com/whitemug/eviction-guard/pkg/plugin` — registration
- `github.com/whitemug/eviction-guard/pkg/filters` — `nodeFilter` evaluation
- `github.com/whitemug/eviction-guard/pkg/signals` — disruption detectors
- `github.com/whitemug/eviction-guard/pkg/backends` — capacity mutation (replicas, minReplicas, CR field)

Extension guide: [`docs/04-extension.md`](docs/04-extension.md).

Please read [`CONTRIBUTING.md`](CONTRIBUTING.md) and the [`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md) before opening a PR.

## Metrics

| Metric | Meaning |
|---|---|
| `evg_matched_nodes{policy}` | Nodes passing `nodeFilter` |
| `evg_vulnerable_nodes{policy}` | Filtered nodes with a disruption signal |
| `evg_current_spare{policy,namespace,workload}` | Extra replicas currently held |
| `evg_scale_actions_total{policy,direction,backend,result}` | Scale-up / scale-back count |
| `evg_spare_not_ready{policy,namespace,workload}` | 1 while extra pods are not Ready off the dying node |

## Status

v1alpha1 controller. Design background: [`docs/01-problem.md`](docs/01-problem.md), [`docs/02-solution-design.md`](docs/02-solution-design.md). Install: [`docs/03-install.md`](docs/03-install.md).

## License

MIT — see [LICENSE](LICENSE).
