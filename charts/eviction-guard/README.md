# eviction-guard Helm chart

Proactive spare capacity and `pods/eviction` gating for predicted node disruption (Karpenter, Spot, cordon).

## Install

```bash
helm install eviction-guard oci://ghcr.io/whitemug/charts/eviction-guard \
  --version 0.2.2 \
  --namespace eviction-guard-system --create-namespace
```

From this repository: `helm install eviction-guard charts/eviction-guard -n eviction-guard-system --create-namespace`.

Requires Kubernetes **1.27+**. The chart does not create a policy by default; apply `examples/` or set `defaultPolicy.enabled=true`.

## Important defaults

| Value | Default | Notes |
|---|---|---|
| `replicaCount` | `2` | Webhook HA; override with `--set replicaCount=1` for tiny/dev |
| `webhook.enabled` | `true` | Eviction gating; keep on |
| `webhook.failurePolicy` | `Fail` | Webhook outage blocks drains of opted-in pods |
| `webhook.certDurationDays` | `365` | Upgrades **reuse** the TLS Secret; delete it to reissue, or use cert-manager (Kustomize path) |
| `metrics.bindAddress` | `:8080` | Plaintext, unauthenticated — restrict with NetworkPolicy if needed |
| `defaultPolicy.enabled` | `false` | Bare install does not watch the whole cluster |
| `extraClusterRoleRules` | `[]` | Required for custom catalog backends that EVG patches (app CRs, KEDA Recipe A, …) |

Full operator docs: [docs/install.md](../../docs/install.md), [docs/configure.md](../../docs/configure.md), [docs/keda.md](../../docs/keda.md), [docs/gitops.md](../../docs/gitops.md).

## Values

See comments in [values.yaml](values.yaml). Common overrides:

```bash
helm upgrade --install eviction-guard oci://ghcr.io/whitemug/charts/eviction-guard \
  --version 0.2.2 -n eviction-guard-system \
  --set replicaCount=1 \
  --set resources.requests.memory=128Mi
```
