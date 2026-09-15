# Changelog

All notable changes to this project are documented here. Versions follow [SemVer](https://semver.org/); the CRD API is `v1alpha1` and may still change before a beta.

## [Unreleased]

### Added

- Deferred disruption windows when `maxConcurrentWindows` is hit: lightweight no-scale windows that still arm `maxWindow` / `ForcedCool` so drains fail-open instead of denying forever without a clock.
- Split webhook `failurePolicy` knobs (`evictionFailurePolicy` default `Fail`, `deploymentFailurePolicy` default `Ignore`) with clear docs that eviction Fail is cluster-wide.
- Optional Helm metrics Service / NetworkPolicy; `webhook.certManager.enabled` path; chart `NOTES.txt`.
- Ready probes wait for cache sync (and webhook server when enabled).
- Release: Trivy scan of a local amd64 image **before** push; SPDX SBOM (syft) attached to the GitHub Release. CI: `helm template | kubeconform`.
- Design inventory for Target abstraction beyond Deployment: [docs/design-targets.md](docs/design-targets.md).

### Changed

- Window create / patch happens **before** capacity scale-up (avoids orphaned replicas with no window / ForcedCool clock).
- Shared `pkg/workload.OwnerDeployment` for policy controller and eviction gate.
- `scaleBackAndClose` no longer double-calls restore (single restore via `scaleBackAndUnfinalize`).
- Eviction webhook falls back to request metadata only when `Object.raw` is empty; logs the original decode error.
- Signals registry uses `sync.RWMutex`; `IsActive` empty-phase behavior documented in API godoc.
- Makefile `test-unit` skips generate/manifests for a faster local loop.

### Fixed

- Indexed pod list errors no longer fall back to cluster-wide Pod lists.
- RBAC grants `events.k8s.io` Events for controller-runtime 0.25 recorders.
- E2E pins `replicaCount=1` with `leaderElect=false`.
- `--webhook-enabled=true` requires `--webhook-cert-dir` (fail-fast).
- Helm CRD upgrade steps documented (`UPGRADING.md` / install).
- Kustomize manager defaults aligned to 2 replicas + PDB.

## [0.2.2] — 2026-09-13

### Added

- Window condition `CapacityApplied` and metric `evg_capacity_apply_error` when a catalog capacity patch fails (admission/RBAC/conflict/other). Windows still open so eviction stays gated; drains fail-open after `maxWindow` (`ForcedCool`). Spot example sets `maxWindow: 15m`.
- Scale-up / restore error handling is Kind-agnostic: classify API errors, surface on the Window, and never special-case HPA/KEDA types in the controllers.
- Catalog entry `skipDownscaling`: raise capacity on disruption but leave integer paths at `ScaledTo` when the window closes (stamps still cleared). Prefer scaler floors (HPA/KEDA); use this mainly on `Deployment` if you still bind it and do not want Eviction Guard to yank replicas back. Examples: `workload-hpa.yaml` (HPA-only bind) and `policy-custom-backend.yaml` (`skipDownscaling` with multi-backend).

### Changed

- Scale-back always reverts each action to its recorded **baseline** (unless `skipDownscaling`). Removed load-aware Held / observed-replicas restore clamps — sticky capacity is an explicit catalog choice, not inferred from Deploy replica count.
- Helm chart default `replicaCount` is **2** so the validating webhook stays available during rolling upgrades / node drains (`failurePolicy: Fail`). Override with `--set replicaCount=1` for tiny/dev clusters.
- Chart / image / appVersion bumped to **0.2.2**.

### Removed

- HPA-only `CapacityHeadroom` / `evg_capacity_ceiling` and typed HPA minReplicas restore helpers (`RestoreMinReplicas` / `ScaleDownMinReplicas`).
- Automatic `Held` phase when Target observed replicas moved past spare (use `skipDownscaling` instead).

## [0.2.1] — 2026-09-10

### Changed

- Copyright holder updated to **Whitemug** (MIT unchanged). Source headers and Helm chart maintainers align with the GitHub org.
- Bump `sigs.k8s.io/controller-runtime` to **0.25.0** (and Kubernetes client libraries to **0.37**). Migrate API scheme registration off deprecated `controller-runtime/pkg/scheme.Builder`, and event emission to `GetEventRecorder` / `events.k8s.io` (`emitf` helper). Envtest tooling tracks `release-0.25` with Kubernetes **1.37** binaries.
- Chart / image / appVersion bumped to **0.2.1**. Artifact Hub `prerelease` set to `true` while the API remains `v1alpha1`.

### Fixed

- Eviction webhook no longer fail-opens on non-NotFound pod `Get` errors (timeouts/Forbidden); only true NotFound allows.
- Eviction gate allows pods on non-vulnerable nodes even while a sibling disruption window is open (matches documented behavior).
- Window reconciler scales back remaining backends and closes when the target Deployment is deleted, instead of waiting until `maxWindow`.
- Policy reconcile propagates owner-resolution API errors instead of silently skipping at-risk pods.
- Multi-path HPA restore (`minReplicas`+`maxReplicas`) applies the G4 `currentReplicas` floor; restore skips missing objects and continues with remaining actions.
- Consecutive integer patches on the same object (e.g. HPA `minReplicas` + `maxReplicas`) apply as one merge patch so API validation stays valid.
- Scale-floor hold no longer treats HPA `currentReplicas` above `ScaledTo` as load when it is still at/below the HPA min floor we set.

### Added

- Kind backends e2e (`make test-kind-backends`): Deployment, HPA min+max, KEDA Recipe A (patch min), and Recipe B (Prometheus / empty patches). Wired in CI as `e2e-backends`.

## [0.2.0] — 2026-09-08

First public-oriented redesign cut. **Breaking** relative to any prior `0.1.x` chart/image that used `defaultBackend` / REST `scale-target` / PDB-style hold.

### Breaking

- Policy capacity is a required `spec.backends` catalog (named keys → apiVersion/kind/path). Removed `defaultBackend` / `additionalBackends` and the REST `scale-target` annotation.
- Workload pin annotation is `eviction-guard.io/policy-pin` (was the same string as the Window label `eviction-guard.io/policy`).
- Window `ScaleAction.backend` kind-class removed; patch order follows the workload `scale-backend` annotation (first key is SpareReady primary).
- `eviction-guard.io/scale-backend` is **required** on protected Deployments (no implicit Deployment-kind catalog bind).
- Eviction gating uses a validating webhook on `pods/eviction` (not a PDB-style hold).
- Chart / image / appVersion bumped to **0.2.0**.

### Added / locked

- Multi-policy ownership (`pkg/policyown`), backend bindings (`eviction-guard.io/scale-backend`).
- Backend catalog key DNS-1123 validation; catalog entries may list multiple integer `path`s (e.g. HPA min+max) or **omit patches** for external scalers (Window + metrics only); annotation-only patches allowed.
- Workload gauges `evg_at_risk_pods` and `evg_desired_replicas` (for observability / external scalers).
- Helm `kubeVersion: ">=1.27.0-0"`; webhook cert default lifetime **365** days (upgrades still reuse the existing Secret).
- Go module / Dockerfile toolchain **1.27.1**.
- Dropped unused Go filter registry (`RegisterFilter`); node coverage is `spec.nodeFilter` / `customSignals` only.

### Docs

- Operator docs rewritten for catalog + eviction webhook.
- Design note: [docs/design-scale-targets.md](docs/design-scale-targets.md).
- Metrics: [docs/metrics.md](docs/metrics.md). GitOps: [docs/gitops.md](docs/gitops.md). KEDA: [docs/keda.md](docs/keda.md).
- Migration: [UPGRADING.md](UPGRADING.md).

## [0.1.1] — 2026-09-01

Pre-redesign patch (private / early). See git history for details.

## [0.1.0] — 2026-08-31

Initial private release.
