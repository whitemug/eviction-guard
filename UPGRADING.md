# Upgrading

`api/v1alpha1` is alpha. Treat chart `0.2.x` as a **new install contract**, not an in-place compatible bump from `0.1.x`.

## Helm CRDs (every upgrade)

Helm 3 installs chart `crds/` on **first install only** — `helm upgrade` does **not** update CRDs. After any release that changes CRD schemas, apply them yourself before or with the chart upgrade:

```bash
kubectl apply -f charts/eviction-guard/crds/
# or from a checked-out tag / release asset that ships the same YAML
helm upgrade --install eviction-guard oci://ghcr.io/whitemug/charts/eviction-guard \
  --version <new> -n eviction-guard-system
```

Chart CRDs are copied from `config/crd/bases` via `make manifests` / `make helm-crds`; keep them in sync when changing the API.

## Webhook TLS certs

Helm’s default path generates a self-signed Secret and **reuses** it across upgrades (`lookup`). Certs expire after `webhook.certDurationDays` (default **365**). Before expiry:

```bash
kubectl delete secret -n eviction-guard-system eviction-guard-webhook-certs
helm upgrade --install eviction-guard ...   # regenerates Secret + caBundle
```

Or set `webhook.certManager.enabled=true` (requires [cert-manager](https://cert-manager.io/); chart creates Issuer+Certificate and annotates the VWC). Kustomize installs already use `config/certmanager`.

## From 0.2.2 → 0.2.3

In-place chart/image upgrade is supported (same CRD API; no Policy/Workload annotation migration). Apply chart CRDs first — this release adds fields (see [Helm CRDs](#helm-crds-every-upgrade)).

1. Upgrade to chart/image **0.2.3**.
2. Webhook `failurePolicy` is now three knobs: `evictionFailurePolicy` (default `Fail`, cluster-wide on `pods/eviction`), `deploymentFailurePolicy` (default `Ignore`), and `policyFailurePolicy` (default `Fail`). The old `webhook.failurePolicy` value still applies to all three if you set it, but prefer migrating to the split knobs — a webhook outage now only blocks evictions cluster-wide, not Deployment rollouts.
3. When `maxConcurrentWindows` is saturated, a newly at-risk workload now gets a lightweight **deferred window** (`eviction-guard.io/deferred` label, no capacity patch) instead of nothing. It still arms `maxWindow`/`ForcedCool` so eviction eventually fail-opens rather than denying indefinitely with no clock. Deferred windows do not count against `maxConcurrentWindows`; watch `evg_deferred_workloads` if you alert on window counts. **Do not combine a finite `maxConcurrentWindows` with `maxWindow: 0` (unlimited)** — deferred fail-open depends on ForcedCool, so that pair can deny drains until a scaling slot frees.
4. Window creation now happens **before** the capacity scale-up patch (previously the reverse). No action needed, but Window creation errors that used to be silent-then-orphan a scale-up now surface immediately with no capacity change applied.
5. New chart objects land **enabled by default** on upgrade (not opt-in): `metrics.service.enabled` (default `true` → creates `eviction-guard-metrics`) and `networkPolicy.enabled` (default `true` → adds a `NetworkPolicy` that is a port allow-list for the manager's own health/metrics/webhook ports with no `from` restriction, so it is a no-op for any traffic that worked before and only closes off ports the manager does not use; harmless if your CNI does not enforce `NetworkPolicy`). Disable with `--set metrics.service.enabled=false` and/or `--set networkPolicy.enabled=false` if you do not want those objects yet. Also available: `webhook.certManager.enabled` (default `false`, use cert-manager instead of the Helm-generated cert).
6. CI/release changes only affect maintainers publishing this repo: Trivy now scans the local image before push (not the pushed digest), and an SPDX SBOM is attached to the GitHub Release; `helm template | kubeconform` runs in CI.

## From 0.2.1 → 0.2.2

In-place chart/image upgrade is supported (same CRD API; no Policy/Workload annotation migration). Still apply chart CRDs if you prefer an explicit refresh (see above).

1. Upgrade to chart/image **0.2.2**.
2. Helm default `replicaCount` is now **2** (webhook HA under eviction `failurePolicy: Fail`). The chart adds a PDB when `replicaCount > 1`. Use `--set replicaCount=1` if you want a single manager pod.
3. Watch for new Window condition **`CapacityApplied`** and metric **`evg_capacity_apply_error`** when a catalog capacity patch fails. Eviction still fail-opens after `maxWindow` (`ForcedCool`); Spot policies often use `maxWindow: 15m`.
4. **Removed** metric `evg_capacity_ceiling` (and HPA-only headroom helpers). Point dashboards/alerts at `evg_capacity_apply_error` plus Window `CapacityApplied` / Events instead.
5. Scale-back now always restores catalog paths to **baseline**. The old load-aware `Held` / observed-replicas clamp is gone. Prefer binding **HPA or KEDA** floors (not `Deployment.replicas`) when a scaler owns the workload. If you still bind Deployment and want sticky replicas after the window, set `skipDownscaling: true` on that catalog entry.

## From 0.2.0 → 0.2.1

In-place chart/image upgrade is supported (same CRD API).

1. Upgrade to chart/image **0.2.1** (includes the eviction webhook fail-open fix and related gate/window restore fixes).
2. No Policy/Workload annotation changes required for this bump.
3. If you still run `0.2.0`, prefer upgrading before relying on the gate under API/cache errors — older builds could allow eviction when pod `Get` failed for non-NotFound reasons.

## From 0.1.x → 0.2.0

1. **Drain / quiet the cluster** (or accept that open windows will scale back when the old controller stops).
2. Remove old Policies / Windows if they still use removed fields (`defaultBackend`, Window `spec.backend`, REST `scale-target`).
3. Upgrade the chart/image to `0.2.x`. **Apply CRDs explicitly** (`kubectl apply -f charts/eviction-guard/crds/`) — Helm 3 does not upgrade `crds/` on `helm upgrade` (see [Helm CRDs](#helm-crds-every-upgrade)).
4. Re-apply Policies with required `spec.backends`, for example:

```yaml
spec:
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

5. On Deployments:
   - Keep `eviction-guard.io/protected: "true"` on the pod template.
   - Rename pin annotation `eviction-guard.io/policy` → `eviction-guard.io/policy-pin` if you used pinning.
   - **Always** set `eviction-guard.io/scale-backend` (required). Example: `deployment` or `deployment,hpa`.
     Token order is patch order (scale-up and scale-back); the first key is the SpareReady primary.
     There is no default Deployment bind when the annotation is omitted.
6. Grant `extraClusterRoleRules` for custom catalog GVKs.
7. Confirm the validating webhook is present and per-webhook `failurePolicy` values are intentional (`evictionFailurePolicy: Fail` rejects all `pods/eviction` CREATE cluster-wide if the webhook is down).

## Compatibility promise (alpha)

| Stable until we say otherwise | May change before v1beta1 |
|---|---|
| Label/annotation **strings** in `api/v1alpha1/labels.go` (after 0.2.0 renames) | CRD field shapes inside `v1alpha1` |
| Metric names (`evg_*`) | Go package layouts under `pkg/` except listed contracts |
| `pkg/plugin.RegisterSignal`, `pkg/signals.Detector`, `pkg/evictgate`, `pkg/policyown`, catalog helpers in `pkg/backends` | Internal packages and unexported helpers |

See [CONTRIBUTING.md](CONTRIBUTING.md) and [CHANGELOG.md](CHANGELOG.md).
