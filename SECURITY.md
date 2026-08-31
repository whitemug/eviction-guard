# Security Policy

## Supported versions

Alpha (`v0.x`, API `v1alpha1`) receives fixes on `main` only.

## Reporting a vulnerability

Please **do not** open a public issue for security reports.

Contact [Vikas Verma](https://github.com/vikasvr), or open a [private security advisory](https://github.com/whitemug/eviction-guard/security/advisories/new) if you have access.

Include:

- Affected version / commit
- Cluster impact (RBAC, scale-up runaway, denial of disruption protection)
- Reproduction notes

## Trust boundary

Eviction Guard is a cluster-scoped operator. It can patch Deployments and HPAs. Treat `EvictionGuardPolicy` create/update as a privileged action; restrict it with RBAC the same way you restrict HPA or ClusterAutoscaler objects.

## Signed releases

Tagged images (`ghcr.io/whitemug/eviction-guard`) and Helm OCI charts (`ghcr.io/whitemug/charts/eviction-guard`) are signed with Cosign keyless (GitHub Actions OIDC). See [`docs/05-publishing.md`](docs/05-publishing.md) for `cosign verify` commands.

## Scanning

- **Go code:** `govulncheck ./...` on every PR.
- **Container image:** Trivy on the image built from `Dockerfile` (`HIGH`/`CRITICAL`, ignore unfixed). The same scan runs on tagged releases (the pushed GHCR digest, before Cosign) and weekly on Mondays so new base-image CVEs show up without a code change. Results are uploaded to the GitHub **Security** tab (Code scanning).
- **Dependencies:** Dependabot watches `go.mod`, the Dockerfile, and GitHub Actions.
