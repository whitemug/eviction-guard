# Eviction Guard

[![CI](https://github.com/whitemug/eviction-guard/actions/workflows/ci.yaml/badge.svg)](https://github.com/whitemug/eviction-guard/actions/workflows/ci.yaml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![GitHub release](https://img.shields.io/github/v/release/whitemug/eviction-guard?include_prereleases)](https://github.com/whitemug/eviction-guard/releases)
[![Go Reference](https://pkg.go.dev/badge/github.com/whitemug/eviction-guard.svg)](https://pkg.go.dev/github.com/whitemug/eviction-guard)

Proactive protection for **voluntary node drains**: scale opted-in Deployments so spare pods are Ready **before** eviction, then let drain proceed and scale back.

A validating webhook on `pods/eviction` denies eviction of opted-in pods until spare capacity is Ready (one at-risk pod at a time). Node signals (Karpenter, cordon, …) only start scale-up earlier. `eviction-guard.io/protected` on the pod template is **opt-in**, not a lock.

## Docs

| | |
|---|---|
| **[What it does](docs/overview.md)** | Product model and non-goals |
| **[Install](docs/install.md)** | Helm / Kustomize |
| **[Configure](docs/configure.md)** | Policies, workloads, signals |
| **[How-to](docs/howto.md)** | Simulate disruption, gating, troubleshoot |
| **[Metrics](docs/metrics.md)** | Prometheus gauges / counters |
| **[KEDA](docs/keda.md)** | Patch ScaledObject or external + metrics |
| **[GitOps](docs/gitops.md)** | Argo CD / Flux ignore capacity during windows |
| **[All docs](docs/README.md)** | Index |

## Quick start

```bash
helm install eviction-guard oci://ghcr.io/whitemug/charts/eviction-guard \
  --version 0.2.1 \
  --namespace eviction-guard-system --create-namespace
kubectl apply -f examples/policy-spot.yaml
kubectl apply -f examples/workload.yaml
```

From a clone: `helm install eviction-guard charts/eviction-guard -n eviction-guard-system --create-namespace`.

Minimal workload opt-in:

```yaml
metadata:
  annotations:
    eviction-guard.io/scale-backend: deployment
spec:
  template:
    metadata:
      labels:
        eviction-guard.io/protected: "true"
```

Minimal policy (Spot capacity filter; built-in signals include Karpenter / cordon — add `SpotInterrupted` when you want Spot interruption coverage, see [examples/policy-spot.yaml](examples/policy-spot.yaml)):

```yaml
apiVersion: eviction-guard.io/v1alpha1
kind: EvictionGuardPolicy
metadata:
  name: spot-workers
spec:
  nodeFilter:
    capacityTypes: ["spot"]
  spareReplicas: 1
  maxBuffer: 4
  scaleBackAfter: 1m
  backends:
    deployment:
      apiVersion: apps/v1
      kind: Deployment
      patches:
        - path: spec.replicas
    hpa:
      apiVersion: autoscaling/v1
      kind: HorizontalPodAutoscaler
      patches:
        - path: spec.minReplicas
```

More examples in [`examples/`](examples/README.md). Signal recipes: [docs/signals.md](docs/signals.md).

## How it works

```
Node signal / cordon
        │
        ▼
EvictionGuardPolicy (nodeFilter) → scale spare → EvictionGuardWindow
        │
        ▼
pods/eviction webhook: deny until SpareReady → allow one at-risk pod
        │
        ▼
scale back → Cooling → close
```

## Metrics

| Metric | Meaning |
|---|---|
| `evg_matched_nodes{policy}` | Nodes passing `nodeFilter` |
| `evg_vulnerable_nodes{policy}` | Filtered nodes with a disruption signal |
| `evg_at_risk_pods{policy,namespace,workload}` | Protected pods on vulnerable nodes |
| `evg_desired_replicas{policy,namespace,workload}` | Capacity target for the workload |
| `evg_current_spare{policy,namespace,workload}` | Extra replicas currently open |
| `evg_scale_actions_total{policy,direction,backend,result}` | Scale-up / scale-back (`backend` = catalog key) |
| `evg_spare_not_ready{policy,namespace,workload}` | 1 while spare not Ready off dying nodes |
| `evg_deferred_workloads{policy}` | Waiting for a window slot |
| `evg_max_window_exceeded_total{...}` | Force-cooled by `maxWindow` |
| `evg_eviction_decisions_total{decision}` | Webhook allow/deny |

Full reference: [docs/metrics.md](docs/metrics.md).

## Integrate

| Layer | What you do |
|---|---|
| CRDs | Apply `EvictionGuardPolicy`; watch `EvictionGuardWindow` |
| Workload | `protected` label + required `scale-backend` annotation |
| Go module | `plugin.RegisterSignal` (capacity via Policy catalog; node coverage via `nodeFilter`) |

See [docs/extension.md](docs/extension.md). Building from source needs **Go 1.27.1** (`go.mod`).

## Status

v1alpha1 (`0.2.1`). Migration: [UPGRADING.md](UPGRADING.md). Design: [docs/design.md](docs/design.md).

## Community

- [Contributing](CONTRIBUTING.md)
- [Support](SUPPORT.md)
- [Security](SECURITY.md)
- [Code of Conduct](CODE_OF_CONDUCT.md)
- [Maintainers](MAINTAINERS)

## License

MIT — see [LICENSE](LICENSE).
