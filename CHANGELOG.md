# Changelog

All notable changes to this project are documented here. Versions follow [SemVer](https://semver.org/); the CRD API is `v1alpha1` and may still change before a beta.

## [Unreleased]

### Fixed

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
