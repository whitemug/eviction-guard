# Examples

Apply a policy and a matching workload after installing the operator ([docs/install.md](../docs/install.md)).

| File | Purpose | Pair with |
|---|---|---|
| [policy-spot.yaml](policy-spot.yaml) | Spot capacity filter + SpotInterrupted and built-in signals | [workload.yaml](workload.yaml) or [workload-hpa.yaml](workload-hpa.yaml) |
| [policy-cordon.yaml](policy-cordon.yaml) | Cordon-focused policy | [workload.yaml](workload.yaml) |
| [policy-named-pool.yaml](policy-named-pool.yaml) | Label-selected node pool | [workload.yaml](workload.yaml) |
| [policy-custom-signals.yaml](policy-custom-signals.yaml) | `customSignals` recipes | [workload.yaml](workload.yaml) |
| [policy-custom-backend.yaml](policy-custom-backend.yaml) | Extra catalog backend + `skipDownscaling` on Deployment | [workload-multi-backend.yaml](workload-multi-backend.yaml) |
| [workload.yaml](workload.yaml) | Minimal Deployment opt-in (`protected` + `scale-backend: deployment`) | any policy above |
| [workload-hpa.yaml](workload-hpa.yaml) | HPA-owned capacity (`scale-backend: hpa`) — preferred when an HPA exists | [policy-spot.yaml](policy-spot.yaml) |
| [workload-multi-backend.yaml](workload-multi-backend.yaml) | Multi-key `scale-backend` (Deploy + HPA) | [policy-custom-backend.yaml](policy-custom-backend.yaml) |
| [policy-keda-minreplicas.yaml](policy-keda-minreplicas.yaml) | Patch KEDA ScaledObject `minReplicaCount` | [workload-keda.yaml](workload-keda.yaml) |
| [policy-keda-external.yaml](policy-keda-external.yaml) | External scaler + metrics only (no patches) | [workload-keda.yaml](workload-keda.yaml), [scaledobject-evg-metrics.yaml](scaledobject-evg-metrics.yaml) |
| [workload-keda.yaml](workload-keda.yaml) | Workload annotated for KEDA backends | KEDA policies above |
| [scaledobject-evg-metrics.yaml](scaledobject-evg-metrics.yaml) | Sample ScaledObject scraping `evg_*` metrics | [policy-keda-external.yaml](policy-keda-external.yaml) |
| [argocd-ignore-differences.yaml](argocd-ignore-differences.yaml) | Argo CD ignore rules for capacity during windows | GitOps installs ([docs/gitops.md](../docs/gitops.md)) |

When a scaler owns the workload, prefer binding **HPA** or **KEDA** (`workload-hpa.yaml` / KEDA examples), not `Deployment.replicas`. If you still raise Deployment alongside a scaler, set `backends.deployment.skipDownscaling: true` so Eviction Guard does not yank replicas on close.

Kubebuilder also ships a starter under [`config/samples/`](../config/samples/); prefer these `examples/` files for day-to-day tryouts.
