# Maintainer checklist (publishing)

What is already in the tree versus what you do on GitHub when cutting a release or going public.

## In this repository

- MIT license, Contributor Covenant, CONTRIBUTING, SUPPORT, SECURITY, CHANGELOG, UPGRADING, MAINTAINERS, CODEOWNERS
- Go module `github.com/whitemug/eviction-guard` (`api/v1alpha1`, `pkg/`)
- CRDs, Helm chart (+ chart README), Kustomize package
- CI: unit tests, envtest, golangci-lint, govulncheck, `helm lint` / `helm template`, `helm template | kubeconform`, kind e2e, Trivy image scan, docs site (`website/`)
- Tag-driven release: GHCR image, Helm OCI chart, Trivy scan of the local amd64 image **before** push, SPDX SBOM (syft) attached to the GitHub Release, Cosign keyless signatures, GitHub Release from CHANGELOG
- Dependabot: Go modules, Dockerfile, GitHub Actions

## Current release state

Prefer **`v0.2.3`** (or newer) for public install — deferred disruption windows under `maxConcurrentWindows`, window-before-scale ordering, split `evictionFailurePolicy` / `deploymentFailurePolicy` / `policyFailurePolicy`, Helm metrics Service + NetworkPolicy (both **default on**), and chart default `replicaCount: 2` for webhook HA. Image and chart on GHCR are public and Cosign-signed on tag.

## Go public

1. Make `whitemug/eviction-guard` **public**. Enable Issues, Discussions, Actions → GHCR, and **Dependabot**. Code scanning (Trivy SARIF) is free once public. Enable **forking**.
2. Confirm description/topics (`kubernetes`, `operator`, `karpenter`, `autoscaling`, `eviction`, `webhook`, `helm`). Org may need to allow forking for public repos.
3. Make GHCR packages **`eviction-guard`** and **`charts/eviction-guard`** public (or inherit from the source repository). Verify:
   ```bash
   helm pull oci://ghcr.io/whitemug/charts/eviction-guard --version 0.2.3
   ```
4. Protect `main`: require PR + CI checks (`test`, `image / trivy`, `e2e`, `e2e-backends`); block force-push/delete.
5. List the chart on Artifact Hub (annotations are already on the chart; `prerelease: true` while the API is `v1alpha1`). Confirm the public install command pulls before announcing it.

## Public site

[karpenter.sh](https://karpenter.sh) is the reference for the shape: a homepage, a docs sidebar, search, and GitHub in the nav. This repo uses a small Hugo site in [`website/`](https://github.com/whitemug/eviction-guard/tree/main/website), with one docs version.

`docs/` stays the source of truth. Hugo mounts those files (and the root project docs operators already link to) and fails the build when a relative link does not resolve. Maintainer notes stay out of the Guides list; they are under Project.

The homepage carries the Helm install for `params.version`, the drain sequence, the PDB / HPA comparison, and the non-goals. One docs version. GitHub Pages serves `https://whitemug.github.io/eviction-guard/`.

Preview: `make site-serve` (http://127.0.0.1:1313). That downloads Hugo 0.139.4 into `bin/` when it is not already there, then builds the Pagefind index so search works in the preview. `make site` is the GitHub Pages build: same Hugo config, production `baseURL`, then Pagefind. Search reads that static index in the browser. Hugo’s live-reload server is not used, because it would rebuild the HTML and drop the index.

One-time: in the repo settings, set Pages → Build and deployment → Source to **GitHub Actions**. The `website` workflow builds on every pull request and deploys from `main`.

## Cut a new version

Example below uses **`v0.2.3`**; for a later cut, substitute the new version everywhere (Chart.yaml, values, kustomize `newTag`, CHANGELOG, tag name).

1. Bump in lockstep with the git tag (the release job fails if Chart.yaml / `image.tag` / kustomize `newTag` drift):
   - `charts/eviction-guard/Chart.yaml` `version` and `appVersion`
   - `charts/eviction-guard/values.yaml` `image.tag`
   - `config/default/kustomization.yaml` `newTag` (keep samples aligned)
   - `CHANGELOG.md` + docs install examples
   - `website/hugo.yaml` `params.version` (homepage install command)
2. Merge to `main`, then tag:
   ```bash
   git checkout main && git pull
   git tag v0.2.3
   git push origin v0.2.3
   ```
   That publishes:
   - Image `ghcr.io/whitemug/eviction-guard:0.2.3` (and the `0.2` minor tag)
   - Chart `oci://ghcr.io/whitemug/charts/eviction-guard:0.2.3`
   - A GitHub Release whose body is the matching CHANGELOG section (create-or-update on re-run)
   Both artifacts are signed with Cosign keyless (GitHub OIDC → Sigstore). Helm GPG `.prov` files are not used.
   The release workflow also refuses to publish unless a successful `ci.yaml` run already exists for the tagged commit.
3. Compatibility: `v1alpha1` may change; see [UPGRADING.md](../UPGRADING.md) and [CONTRIBUTING.md](../CONTRIBUTING.md).

### Pre-tag reminders

- Metrics bind to `:8080` with no auth — Helm defaults create a metrics Service and a port-only NetworkPolicy; tighten further for multi-tenant clusters.
- Helm webhook TLS Secret is sticky across upgrades (`lookup`); delete the Secret then `helm upgrade` to rotate, or use `webhook.certManager.enabled` / `config/certmanager`.
- Eviction + Deployment validating webhooks are cluster-scoped (no namespace selector by default). Per-webhook failure policies: eviction/policy default `Fail`, deployment default `Ignore`.
- Helm 3 does not upgrade chart `crds/` — `kubectl apply` CRDs on schema changes ([UPGRADING.md](../UPGRADING.md)).

Install:

```bash
helm install eviction-guard oci://ghcr.io/whitemug/charts/eviction-guard \
  --version 0.2.3 -n eviction-guard-system --create-namespace
```

Verify signatures (Cosign 2+):

```bash
cosign verify ghcr.io/whitemug/eviction-guard:0.2.3 \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp 'https://github.com/whitemug/eviction-guard/.github/workflows/release.yaml@refs/tags/v.*'

cosign verify ghcr.io/whitemug/charts/eviction-guard:0.2.3 \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp 'https://github.com/whitemug/eviction-guard/.github/workflows/release.yaml@refs/tags/v.*'
```

Go consumers: `go get github.com/whitemug/eviction-guard@v0.2.3`.

Public import paths: `api/v1alpha1`, `pkg/plugin`, `pkg/filters`, `pkg/signals`, `pkg/evictgate`, `pkg/backends`, `pkg/policyown`, `pkg/naming`. `internal/` is not an API. Metric names (`evg_*`) are the observability contract; prefer not to import `pkg/metrics` from outside.
