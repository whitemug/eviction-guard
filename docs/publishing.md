# Maintainer checklist (publishing)

What is already in the tree versus what you do on GitHub when you cut a public release.

## In this repository

- MIT license, Contributor Covenant, CONTRIBUTING, SUPPORT, SECURITY, CHANGELOG, UPGRADING, MAINTAINERS, CODEOWNERS
- Go module `github.com/whitemug/eviction-guard` (`api/v1alpha1`, `pkg/`)
- CRDs, Helm chart (+ chart README), Kustomize package
- CI: unit tests, envtest, golangci-lint, govulncheck, `helm lint` / `helm template`, kind e2e, Trivy image scan
- Tag-driven release: GHCR image, Helm OCI chart, Trivy on the pushed digest, Cosign keyless signatures, GitHub Release from CHANGELOG
- Dependabot: Go modules, Dockerfile, GitHub Actions

## GitHub / release steps

1. Make `whitemug/eviction-guard` **public**. Enable Issues, Discussions, Actions pushing to GHCR, and **Dependabot**. Code scanning (Trivy SARIF) is free once public; private repos need GitHub Advanced Security.
2. Set a short repo description and topics (`kubernetes`, `operator`, `karpenter`, `autoscaling`, `eviction`, `webhook`).
3. First-time GHCR: make the `eviction-guard` and `charts/eviction-guard` packages **public**, or enable “Inherit access from source repository”.
4. Bump in lockstep with the git tag (the release job fails if they drift):
   - `charts/eviction-guard/Chart.yaml` `version` and `appVersion`
   - `charts/eviction-guard/values.yaml` `image.tag`
   - Update `CHANGELOG.md` / docs install examples
5. Merge to `main`, then tag the release:
   ```bash
   git checkout main && git pull
   git tag v0.2.0
   git push origin v0.2.0
   ```
   That publishes:
   - Image `ghcr.io/whitemug/eviction-guard:0.2.0` (and the `0.2` minor tag)
   - Chart `oci://ghcr.io/whitemug/charts/eviction-guard:0.2.0`
   - A GitHub Release whose body is the matching CHANGELOG section
   Both artifacts are signed with Cosign keyless (GitHub OIDC → Sigstore). Helm GPG `.prov` files are not used.
6. Optional: Artifact Hub listing.
7. Compatibility: `v1alpha1` may change; see [UPGRADING.md](../UPGRADING.md) and [CONTRIBUTING.md](../CONTRIBUTING.md).

### Pre-tag reminders

- Metrics bind to `:8080` with no auth — document NetworkPolicy for hardened clusters.
- Helm webhook TLS Secret is sticky across upgrades (`lookup`); deleting the Secret (or using cert-manager) is how you rotate.
- Eviction + Deployment validating webhooks are cluster-scoped (no namespace selector by default).

Install:

```bash
helm install eviction-guard oci://ghcr.io/whitemug/charts/eviction-guard \
  --version 0.2.0 -n eviction-guard-system --create-namespace
```

Verify signatures (Cosign 2+):

```bash
cosign verify ghcr.io/whitemug/eviction-guard:0.2.0 \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp 'https://github.com/whitemug/eviction-guard/.github/workflows/release.yaml@refs/tags/v.*'

cosign verify ghcr.io/whitemug/charts/eviction-guard:0.2.0 \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp 'https://github.com/whitemug/eviction-guard/.github/workflows/release.yaml@refs/tags/v.*'
```

Go consumers: `go get github.com/whitemug/eviction-guard@v0.2.0`.

Public import paths: `api/v1alpha1`, `pkg/plugin`, `pkg/filters`, `pkg/signals`, `pkg/evictgate`, `pkg/backends`, `pkg/policyown`, `pkg/naming`. `internal/` is not an API. Metric names (`evg_*`) are the observability contract; prefer not to import `pkg/metrics` from outside.
