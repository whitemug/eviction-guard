# Maintainer checklist (publishing)

What is already in the tree versus what you do on GitHub when cutting a release or going public.

## In this repository

- MIT license, Contributor Covenant, CONTRIBUTING, SUPPORT, SECURITY, CHANGELOG, UPGRADING, MAINTAINERS, CODEOWNERS
- Go module `github.com/whitemug/eviction-guard` (`api/v1alpha1`, `pkg/`)
- CRDs, Helm chart (+ chart README), Kustomize package
- CI: unit tests, envtest, golangci-lint, govulncheck, `helm lint` / `helm template`, kind e2e, Trivy image scan
- Tag-driven release: GHCR image, Helm OCI chart, Trivy on the pushed digest, Cosign keyless signatures, GitHub Release from CHANGELOG
- Dependabot: Go modules, Dockerfile, GitHub Actions

## Current release state

Prefer **`v0.2.2`** (or newer) for public install — CapacityApplied apply-error handling, Kind-agnostic scale errors, baseline restore with optional `skipDownscaling`, and chart default `replicaCount: 2` for webhook HA. Image and chart publish to GHCR and are Cosign-signed on tag.
Artifacts may still be **private** on GHCR even when the git repo is public — flip package visibility separately.

## Go public

1. Make `whitemug/eviction-guard` **public**. Enable Issues, Discussions, Actions → GHCR, and **Dependabot**. Code scanning (Trivy SARIF) is free once public. Enable **forking**.
2. Confirm description/topics (`kubernetes`, `operator`, `karpenter`, `autoscaling`, `eviction`, `webhook`, `helm`). Org may need to allow forking for public repos.
3. Make GHCR packages **`eviction-guard`** and **`charts/eviction-guard`** public (or inherit from the source repository). Verify:
   ```bash
   helm pull oci://ghcr.io/whitemug/charts/eviction-guard --version 0.2.2
   ```
4. Protect `main`: require PR + CI checks (`test`, `image / trivy`, `e2e`, `e2e-backends`); block force-push/delete.
5. Optional: Artifact Hub listing (chart already carries `artifacthub.io/*` annotations; `prerelease: true` while API is `v1alpha1`).

## Cut a new version (e.g. `v0.2.3`)

1. Bump in lockstep with the git tag (the release job fails if they drift):
   - `charts/eviction-guard/Chart.yaml` `version` and `appVersion`
   - `charts/eviction-guard/values.yaml` `image.tag`
   - `config/default/kustomization.yaml` `newTag` (keep samples aligned)
   - `CHANGELOG.md` + docs install examples
2. Merge to `main`, then tag:
   ```bash
   git checkout main && git pull
   git tag v0.2.2
   git push origin v0.2.2
   ```
   That publishes:
   - Image `ghcr.io/whitemug/eviction-guard:0.2.2` (and the `0.2` minor tag)
   - Chart `oci://ghcr.io/whitemug/charts/eviction-guard:0.2.2`
   - A GitHub Release whose body is the matching CHANGELOG section (create-or-update on re-run)
   Both artifacts are signed with Cosign keyless (GitHub OIDC → Sigstore). Helm GPG `.prov` files are not used.
3. Compatibility: `v1alpha1` may change; see [UPGRADING.md](../UPGRADING.md) and [CONTRIBUTING.md](../CONTRIBUTING.md).

### Pre-tag reminders

- Metrics bind to `:8080` with no auth — document NetworkPolicy for hardened clusters.
- Helm webhook TLS Secret is sticky across upgrades (`lookup`); deleting the Secret (or using cert-manager) is how you rotate.
- Eviction + Deployment validating webhooks are cluster-scoped (no namespace selector by default).

Install:

```bash
helm install eviction-guard oci://ghcr.io/whitemug/charts/eviction-guard \
  --version 0.2.2 -n eviction-guard-system --create-namespace
```

Verify signatures (Cosign 2+):

```bash
cosign verify ghcr.io/whitemug/eviction-guard:0.2.2 \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp 'https://github.com/whitemug/eviction-guard/.github/workflows/release.yaml@refs/tags/v.*'

cosign verify ghcr.io/whitemug/charts/eviction-guard:0.2.2 \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp 'https://github.com/whitemug/eviction-guard/.github/workflows/release.yaml@refs/tags/v.*'
```

Go consumers: `go get github.com/whitemug/eviction-guard@v0.2.2`.

Public import paths: `api/v1alpha1`, `pkg/plugin`, `pkg/filters`, `pkg/signals`, `pkg/evictgate`, `pkg/backends`, `pkg/policyown`, `pkg/naming`. `internal/` is not an API. Metric names (`evg_*`) are the observability contract; prefer not to import `pkg/metrics` from outside.
