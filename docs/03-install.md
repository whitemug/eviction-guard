# Install and verify

Eviction Guard runs as a manager Deployment with two reconcilers (Policy and Window).

## Prerequisites

- Kubernetes 1.27+ (built against client-go 1.32)
- `kubectl`, and optionally Helm 3
- Go 1.27.0 only if you build from source (`go.mod` pins `go 1.27.0`)
- Workloads opted in with `eviction-guard.io/enabled: "true"` and a PDB (unless `requirePDB: false`)

## Helm

From a tagged release:

```bash
helm install eviction-guard oci://ghcr.io/whitemug/charts/eviction-guard \
  --version 0.1.1 \
  --namespace eviction-guard-system --create-namespace
kubectl apply -f examples/policy-spot.yaml
kubectl apply -f examples/workload.yaml
```

From this repository:

```bash
helm install eviction-guard charts/eviction-guard \
  --namespace eviction-guard-system --create-namespace
kubectl apply -f examples/policy-spot.yaml
kubectl apply -f examples/workload.yaml
```

The chart does not install a policy unless `defaultPolicy.enabled` is true. Prefer applying `examples/` so node coverage is explicit. A policy defaults to `maxConcurrentWindows: 8` and `maxWindow: 2h` (`0` = unlimited on either).

## Controller replicas and resources

Default is **one replica** with **leader election on**. That is the right default: the manager is not on the serving path, and a second pod would idle except during failover.

Use two replicas when you care about a rolling chart upgrade or a control-plane node drain not pausing reconciliation:

```bash
helm upgrade --install eviction-guard oci://ghcr.io/whitemug/charts/eviction-guard \
  --version 0.1.1 \
  --namespace eviction-guard-system --create-namespace \
  --set replicaCount=2
```

Only the leader runs the Policy and Window reconcilers. Standbys still serve the validating webhook. The chart installs a `PodDisruptionBudget` (`minAvailable: 1`) when `replicaCount > 1`. Leave `leaderElect: true` (the default); turning it off with more than one replica will double-reconcile.

CPU/memory requests and limits are already set and are **chart values**, not hardcoded:

| Value | Default |
|---|---|
| `resources.requests.cpu` | `50m` |
| `resources.requests.memory` | `64Mi` |
| `resources.limits.cpu` | `500m` |
| `resources.limits.memory` | `256Mi` |

```bash
helm install eviction-guard charts/eviction-guard -n eviction-guard-system --create-namespace \
  --set resources.requests.memory=128Mi \
  --set resources.limits.memory=256Mi
```

To drop a limit, omit that key in a values file (do not set it to `0`). Optional `nodeSelector`, `tolerations`, and `affinity` are passed through to the Deployment. For two replicas, add pod anti-affinity so they do not land on one node.

Kustomize (`config/manager/manager.yaml`) uses the same 1 replica and the same request/limit numbers; patch that file if you need HA there.

Manager flags (`--leader-elect`, `--metrics-bind-address`, `--health-probe-bind-address`, `--webhook-cert-dir`) are wired from the chart; empty `--webhook-cert-dir` disables the webhook.

## Pod security

Defaults match [restricted](https://kubernetes.io/docs/concepts/security/pod-security-standards/#restricted) Pod Security and `gcr.io/distroless/static:nonroot` (UID/GID `65532`). Every field is a Helm value; the chart does not hardcode a second, stricter profile.

| Value | Default |
|---|---|
| `podSecurityContext.runAsNonRoot` | `true` |
| `podSecurityContext.runAsUser` / `runAsGroup` / `fsGroup` | `65532` |
| `podSecurityContext.seccompProfile.type` | `RuntimeDefault` |
| `securityContext.allowPrivilegeEscalation` | `false` |
| `securityContext.readOnlyRootFilesystem` | `true` |
| `securityContext.capabilities.drop` | `[ALL]` |
| `hostNetwork` | `false` |
| `serviceAccount.automountServiceAccountToken` | `true` (required for the API) |
| `webhook.failurePolicy` | `Fail` |
| `webhook.timeoutSeconds` | `5` |
| `webhook.certDurationDays` | `3650` |
| `metrics.bindAddress` | `:8080` |
| `healthProbe.bindAddress` | `:8081` |

Override with a values file (do not `--set` nested objects unless you intend to replace the whole map). Example: a policy engine that forbids a numeric `runAsUser` and only wants `runAsNonRoot`.

`serviceAccount.create`, `serviceAccount.name`, `serviceAccount.annotations`, `imagePullSecrets`, and `priorityClassName` are also values. Keep `automountServiceAccountToken: true` unless you inject a token yourself.

Verify a published chart or image with Cosign: see [`docs/05-publishing.md`](05-publishing.md).

Restrict nodes at install time:

```bash
helm install eviction-guard charts/eviction-guard -n eviction-guard-system --create-namespace \
  --set defaultPolicy.enabled=true \
  --set defaultPolicy.nodeFilter.capacityTypes[0]=spot
```

## Kustomize

Requires [cert-manager](https://cert-manager.io/) for the validating webhook (Helm generates certs itself).

```bash
make docker-build IMG=ghcr.io/whitemug/eviction-guard:dev
make install
kubectl apply -k config/default
kubectl apply -f examples/policy-spot.yaml
```

Point `config/default/kustomization.yaml` `images` at the image you built.

## Verify

```bash
kubectl get egp
kubectl taint node <node> karpenter.sh/disrupted=:NoSchedule
kubectl get deploy -A -l eviction-guard.io/enabled=true
kubectl get egw -A
kubectl logs -n eviction-guard-system -l control-plane=controller-manager -f
```

Remove the taint, wait `scaleBackAfter` (default 15m), confirm replicas return to baseline.

The same loop on a local kind cluster (2 spot workers, 10s cooldown, simulated drain):

```bash
make test-kind          # docker, kind, kubectl, helm
KEEP=1 make test-kind   # leave the cluster up
```

## Validating webhook

Helm installs a validating webhook by default. It rejects opted-in Deployments whose annotations cannot be applied later (unknown `scale-backend`, unparseable `scale-target` / `hpa-target`, non-duration `scale-back-after`, `crd` without a full GVK target). It also rejects `EvictionGuardPolicy` objects whose `nodeFilter.namePattern` is not a valid glob.

Certs are generated by Helm and stored in a Secret; cert-manager is not required. Disable with `--set webhook.enabled=false`.

Kustomize (`config/default`) installs the same webhook and uses cert-manager to mint serving certs and inject the CA (`cert-manager.io/inject-ca-from`).

## RBAC for the `crd` backend

The default ClusterRole can patch Deployments and HPAs. A workload may list several backends (`eviction-guard.io/scale-backend: deployment,hpa-min,crd`); see `examples/workload-multi-backend.yaml`. If `crd` is in that list, grant extra rules (Helm `extraClusterRoleRules`).
