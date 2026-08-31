# Maintainer checklist (publishing)

What is already in the tree versus what you do on GitHub when you cut the first public release.

## In this repository

- MIT license, Contributor Covenant, CONTRIBUTING, SECURITY
- Go module `github.com/whitemug/eviction-guard` (`api/v1alpha1`, `pkg/`)
- CRDs, Helm chart, Kustomize package
- CI: unit tests, envtest, golangci-lint, govulncheck, `helm lint` / `helm template`, kind e2e, Trivy image scan
- Tag-driven release: GHCR image, Helm OCI chart, Trivy on the pushed digest, Cosign keyless signatures
- Dependabot: Go modules, Dockerfile, GitHub Actions

## GitHub / release steps

1. Make `whitemug/eviction-guard` **public**. Enable Issues, Discussions, Actions pushing to GHCR, and **Dependabot** (Settings → Code security). Code scanning (Trivy SARIF) is free once the repo is public; private repos need GitHub Advanced Security.
2. Bump in lockstep with the git tag (the release job fails if they drift):
   - `charts/eviction-guard/Chart.yaml` `version` and `appVersion`
   - `charts/eviction-guard/values.yaml` `image.tag`
3. Tag the release:
   ```bash
   git tag v0.1.0
   git push origin v0.1.0
   ```
   That publishes:
   - Image `ghcr.io/whitemug/eviction-guard:0.1.0` (and the `0.1` minor tag)
   - Chart `oci://ghcr.io/whitemug/charts/eviction-guard:0.1.0`
   The window CRD was renamed to `EvictionGuardWindow` after `v0.1.0`; the next tag must be `v0.1.1` (chart `version` / `appVersion` / `image.tag` already `0.1.1`). Do not retag `v0.1.0`.
   Both are signed with Cosign keyless (GitHub OIDC → Sigstore). Helm GPG `.prov` files are not used.
4. First-time GHCR: make the `eviction-guard` and `charts/eviction-guard` packages **public** (Settings → Packages), or enable “Inherit access from source repository”.
5. Optional: Artifact Hub listing, GitHub topics (`kubernetes`, `operator`, `karpenter`, `autoscaling`).
6. Compatibility: `v1alpha1` may change; `pkg/plugin` interfaces should not break without a major module version.

Install:

```bash
helm install eviction-guard oci://ghcr.io/whitemug/charts/eviction-guard \
  --version 0.1.0 -n eviction-guard-system --create-namespace
```

Verify signatures (Cosign 2+):

```bash
cosign verify ghcr.io/whitemug/eviction-guard:0.1.0 \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp 'https://github.com/whitemug/eviction-guard/.github/workflows/release.yaml@refs/tags/v.*'

cosign verify ghcr.io/whitemug/charts/eviction-guard:0.1.0 \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp 'https://github.com/whitemug/eviction-guard/.github/workflows/release.yaml@refs/tags/v.*'
```

Go consumers: `go get github.com/whitemug/eviction-guard@v0.1.0`.

Public import paths: `api/v1alpha1`, `pkg/plugin`, `pkg/filters`, `pkg/signals`, `pkg/backends`. `internal/controller` is not an API.
