# Install

Eviction Guard runs as a manager Deployment: Policy + Window reconcilers, plus a validating webhook (annotation checks and `pods/eviction` gating).

## Prerequisites

- Kubernetes 1.27+
- `kubectl`, optionally Helm 3
- Go 1.27.1 only if you build from source (`go.mod`)
- Workloads will need `eviction-guard.io/protected: "true"` on the pod template ([Configure](configure.md))

## Helm (recommended)

Tagged release:

```bash
helm install eviction-guard oci://ghcr.io/whitemug/charts/eviction-guard \
  --version 0.2.3 \
  --namespace eviction-guard-system --create-namespace
kubectl apply -f examples/policy-spot.yaml
kubectl apply -f examples/workload.yaml
```

From this repo:

```bash
helm install eviction-guard charts/eviction-guard \
  --namespace eviction-guard-system --create-namespace
kubectl apply -f examples/policy-spot.yaml
kubectl apply -f examples/workload.yaml
```

The chart does **not** create a policy by default. Apply `examples/` or enable `--set defaultPolicy.enabled=true`. Policy defaults: `maxConcurrentWindows: 8`, `maxWindow: 2h` (`0` = unlimited — avoid unlimited `maxWindow` with a finite concurrent-window cap; see [configure](configure.md#capacity-knobs)).

**Cluster singleton:** one install per cluster. Chart resource names do not include `Release.Name`; a second Helm release collides on ClusterRole / webhook configuration.

Keep **`webhook.enabled=true`** (default). Turning the webhook off disables eviction gating.

### CRDs on upgrade

Helm 3 installs chart `crds/` on first install only. On upgrade, apply CRDs yourself:

```bash
kubectl apply -f charts/eviction-guard/crds/
helm upgrade --install eviction-guard ...
```

See [UPGRADING.md](../UPGRADING.md).

Verify signatures: [Publishing](publishing.md).

## Kustomize

Requires [cert-manager](https://cert-manager.io/) for webhook TLS (Helm generates certs itself, or set `webhook.certManager.enabled=true`).

Default: **2 replicas** + PDB (`minAvailable: 1`), same Fail-webhook HA posture as Helm.

**Parity note:** Kustomize matches Helm on manager replicas, PDB, and webhook failure policies. It does **not** ship the Helm chart's metrics Service (`metrics.service.enabled`) or NetworkPolicy (`networkPolicy.enabled`). Add those yourself (or scrape the pod IP / use a ServiceMonitor) if you need the same observability/hardening surface.

```bash
make docker-build IMG=ghcr.io/whitemug/eviction-guard:dev
make install
kubectl apply -k config/default
kubectl apply -f examples/policy-spot.yaml
```

Point `config/default/kustomization.yaml` `images` at the image you built.

## HA and resources

Default: **`replicaCount: 2`** (webhook HA under `evictionFailurePolicy: Fail`), leader election on. Override with `--set replicaCount=1` for tiny/dev clusters.

```bash
helm upgrade --install eviction-guard oci://ghcr.io/whitemug/charts/eviction-guard \
  --version 0.2.3 \
  --namespace eviction-guard-system --create-namespace
# optional: --set replicaCount=1
```

Only the leader reconciles. Standbys still serve the webhook. With `replicaCount > 1` the chart adds a PDB (`minAvailable: 1`). Leave `leaderElect: true`.

| Value | Default |
|---|---|
| `replicaCount` | `2` |
| `resources.requests.cpu` / `memory` | `50m` / `64Mi` |
| `resources.limits.cpu` / `memory` | `500m` / `256Mi` |

Optional: `nodeSelector`, `tolerations`, `affinity`. Prefer pod anti-affinity so both replicas are not on one node.

## Pod security

Defaults match [restricted](https://kubernetes.io/docs/concepts/security/pod-security-standards/#restricted) PSS and distroless nonroot (`65532`). See chart values for `podSecurityContext` / `securityContext`. Keep `serviceAccount.automountServiceAccountToken: true` unless you inject a token yourself.

| Value | Default |
|---|---|
| `webhook.evictionFailurePolicy` | `Fail` |
| `webhook.deploymentFailurePolicy` | `Ignore` |
| `webhook.policyFailurePolicy` | `Fail` |
| `webhook.timeoutSeconds` | `5` |
| `webhook.certDurationDays` | `365` (Secret reused on upgrade — delete Secret to reissue, or `webhook.certManager.enabled`) |
| `metrics.bindAddress` | `:8080` (plaintext, no auth — chart ships a metrics Service; `networkPolicy.enabled` narrows by port, not by source) |
| `metrics.service.enabled` | `true` |
| `networkPolicy.enabled` | `true` |
| `healthProbe.bindAddress` | `:8081` |

**Important:** `evictionFailurePolicy: Fail` means if the webhook is down, **every `pods/eviction` CREATE is rejected** (cluster-wide), not only opted-in pods. That is intentional for safety. Use `Ignore` only if you accept cold drains during outages. Deployment annotation checks default to `Ignore` so rollouts are not blocked by a webhook outage.

The Deployment and `pods/eviction` validating webhooks are **cluster-scoped** (no namespace selector). Only opted-in workloads are gated on eviction when the webhook answers; annotation checks apply to Deployments cluster-wide when the webhook is enabled.

## RBAC for custom backends

Default ClusterRole patches Deployments and HPAs. For custom catalog backends (app CRs, KEDA, …), add Helm `extraClusterRoleRules` for those API groups (see `docs/extension.md` and `examples/policy-custom-backend.yaml`). Missing rules surface as Policy condition `BackendsAuthorized=False` (`MissingRBAC`) plus scale-up Events.

## Quick verify

```bash
kubectl get egp
kubectl get validatingwebhookconfiguration | grep eviction-guard
kubectl taint node <node> karpenter.sh/disrupted=:NoSchedule
kubectl get egw -A
kubectl logs -n eviction-guard-system -l control-plane=controller-manager -f
```

Local kind loop: `make test-kind` (or `ARGS='--verbose'` / `KEEP=1 make test-kind`).

Next: [Configure](configure.md) · [How-to](howto.md)
