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
  --version 0.2.0 \
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

The chart does **not** create a policy by default. Apply `examples/` or enable `--set defaultPolicy.enabled=true`. Policy defaults: `maxConcurrentWindows: 8`, `maxWindow: 2h` (`0` = unlimited).

Keep **`webhook.enabled=true`** (default). Turning the webhook off disables eviction gating.

Verify signatures: [Publishing](publishing.md).

## Kustomize

Requires [cert-manager](https://cert-manager.io/) for webhook TLS (Helm generates certs itself).

```bash
make docker-build IMG=ghcr.io/whitemug/eviction-guard:dev
make install
kubectl apply -k config/default
kubectl apply -f examples/policy-spot.yaml
```

Point `config/default/kustomization.yaml` `images` at the image you built.

## HA and resources

Default: **one replica**, leader election on.

```bash
helm upgrade --install eviction-guard oci://ghcr.io/whitemug/charts/eviction-guard \
  --version 0.2.0 \
  --namespace eviction-guard-system --create-namespace \
  --set replicaCount=2
```

Only the leader reconciles. Standbys still serve the webhook. With `replicaCount > 1` the chart adds a PDB (`minAvailable: 1`). Leave `leaderElect: true`.

| Value | Default |
|---|---|
| `resources.requests.cpu` / `memory` | `50m` / `64Mi` |
| `resources.limits.cpu` / `memory` | `500m` / `256Mi` |

Optional: `nodeSelector`, `tolerations`, `affinity`. For two replicas, add anti-affinity so both are not on one node.

## Pod security

Defaults match [restricted](https://kubernetes.io/docs/concepts/security/pod-security-standards/#restricted) PSS and distroless nonroot (`65532`). See chart values for `podSecurityContext` / `securityContext`. Keep `serviceAccount.automountServiceAccountToken: true` unless you inject a token yourself.

| Value | Default |
|---|---|
| `webhook.failurePolicy` | `Fail` |
| `webhook.timeoutSeconds` | `5` |
| `webhook.certDurationDays` | `365` (Secret reused on upgrade — delete Secret to reissue) |
| `metrics.bindAddress` | `:8080` (plaintext, no auth — restrict with NetworkPolicy if needed) |
| `healthProbe.bindAddress` | `:8081` |

**Important:** `failurePolicy: Fail` on the eviction webhook means if the webhook is down, **drains of opted-in pods are blocked**. That is intentional for safety. Use `Ignore` only if you accept cold drains during outages.

The Deployment and `pods/eviction` validating webhooks are **cluster-scoped** (no namespace selector). Only opted-in workloads are gated on eviction; annotation checks apply to Deployments cluster-wide when the webhook is enabled.

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
