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

**Cluster singleton:** install **once** per cluster. Chart `fullname` is fixed (`eviction-guard` unless `fullnameOverride` / `nameOverride`); it ignores `Release.Name`. A second release (even in another namespace) collides on ClusterRole and `ValidatingWebhookConfiguration` names.

## Important defaults

| Value | Default | Notes |
|---|---|---|
| `replicaCount` | `2` | Webhook HA; override with `--set replicaCount=1` for tiny/dev |
| `webhook.enabled` | `true` | Eviction gating; keep on |
| `webhook.evictionFailurePolicy` | `Fail` | Webhook outage rejects **all** `pods/eviction` CREATE cluster-wide |
| `webhook.deploymentFailurePolicy` | `Ignore` | Annotation validation only; Ignore keeps Deploy rollouts during outage |
| `webhook.policyFailurePolicy` | `Fail` | Policy CR admission |
| `webhook.failurePolicy` | `""` | Deprecated alias applied to all three unless overridden |
| `webhook.certDurationDays` | `365` | Helm self-signed Secret reused on upgrade; delete Secret to reissue |
| `webhook.certManager.enabled` | `false` | Use cert-manager Issuer+Certificate + CA inject instead of Helm genCA |
| `metrics.service.enabled` | `true` | ClusterIP Service on the metrics port for scrape DX |
| `networkPolicy.enabled` | `false` | Restrict ingress to health / metrics / webhook ports |
| `metrics.bindAddress` | `:8080` | Plaintext, unauthenticated — enable NetworkPolicy if needed |
| `defaultPolicy.enabled` | `false` | Bare install does not watch the whole cluster |
| `extraClusterRoleRules` | `[]` | Required for custom catalog backends that EVG patches (app CRs, KEDA Recipe A, …) |

Full operator docs: [docs/install.md](../../docs/install.md), [docs/configure.md](../../docs/configure.md), [docs/keda.md](../../docs/keda.md), [docs/gitops.md](../../docs/gitops.md). CRD upgrades: [UPGRADING.md](../../UPGRADING.md).

## Values

See comments in [values.yaml](values.yaml). Common overrides:

```bash
helm upgrade --install eviction-guard oci://ghcr.io/whitemug/charts/eviction-guard \
  --version 0.2.2 -n eviction-guard-system \
  --set replicaCount=1 \
  --set resources.requests.memory=128Mi
```
