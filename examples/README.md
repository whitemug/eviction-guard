# Examples

Apply a policy and a matching workload after installing the operator ([docs/install.md](../docs/install.md)).

| File | Purpose | Pair with |
|---|---|---|
| [policy-spot.yaml](policy-spot.yaml) | Spot capacity filter + SpotInterrupted and built-in signals | [workload.yaml](workload.yaml) |
| [policy-cordon.yaml](policy-cordon.yaml) | Cordon-focused policy | [workload.yaml](workload.yaml) |
| [policy-named-pool.yaml](policy-named-pool.yaml) | Label-selected node pool | [workload.yaml](workload.yaml) |
| [policy-custom-signals.yaml](policy-custom-signals.yaml) | `customSignals` recipes | [workload.yaml](workload.yaml) |
| [policy-custom-backend.yaml](policy-custom-backend.yaml) | Extra catalog backend entry | [workload-multi-backend.yaml](workload-multi-backend.yaml) |
| [workload.yaml](workload.yaml) | Minimal Deployment opt-in (`protected` + `scale-backend`) | any policy above |
| [workload-multi-backend.yaml](workload-multi-backend.yaml) | Multi-key `scale-backend` annotation | [policy-custom-backend.yaml](policy-custom-backend.yaml) |
| [policy-keda-minreplicas.yaml](policy-keda-minreplicas.yaml) | Patch KEDA ScaledObject `minReplicaCount` | [workload-keda.yaml](workload-keda.yaml) |
| [policy-keda-external.yaml](policy-keda-external.yaml) | External scaler + metrics only (no patches) | [workload-keda.yaml](workload-keda.yaml), [scaledobject-evg-metrics.yaml](scaledobject-evg-metrics.yaml) |
| [workload-keda.yaml](workload-keda.yaml) | Workload annotated for KEDA backends | KEDA policies above |
| [scaledobject-evg-metrics.yaml](scaledobject-evg-metrics.yaml) | Sample ScaledObject scraping `evg_*` metrics | [policy-keda-external.yaml](policy-keda-external.yaml) |
| [argocd-ignore-differences.yaml](argocd-ignore-differences.yaml) | Argo CD ignore rules for capacity during windows | GitOps installs ([docs/gitops.md](../docs/gitops.md)) |

Kubebuilder also ships a starter under [`config/samples/`](../config/samples/); prefer these `examples/` files for day-to-day tryouts.
