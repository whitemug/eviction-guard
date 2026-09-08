# Security Policy

## Supported versions

Alpha (`v0.x`, API `v1alpha1`) receives fixes on `main` only.

## Reporting a vulnerability

Please **do not** open a public issue or Discussion for security reports.

Prefer a [private security advisory](https://github.com/whitemug/eviction-guard/security/advisories/new).
If you cannot open an advisory, contact the lead maintainer listed in [MAINTAINERS](MAINTAINERS) via GitHub.

Include:

- Affected version / commit
- Cluster impact (RBAC, scale-up runaway, denial of disruption protection)
- Reproduction notes

We aim to acknowledge reports within **7 days** and share a remediation plan or status update within **14 days**.

## Trust boundary

Eviction Guard is a cluster-scoped operator. It can patch Deployments and HPAs, and (with the default webhook) **deny `pods/eviction`** for opted-in pods until spare capacity is Ready. Treat `EvictionGuardPolicy` create/update as a privileged action; restrict it with RBAC the same way you restrict HPA or ClusterAutoscaler objects.

With `webhook.failurePolicy: Fail` (Helm default), a webhook outage blocks voluntary drains of opted-in pods until the webhook recovers. That is intentional; do not set `Ignore` unless you accept cold drains during outages. See [docs/howto.md](docs/howto.md).

The manager metrics endpoint (default `:8080`) is plaintext and unauthenticated. Restrict access with NetworkPolicy (or bind to localhost and scrape via a sidecar) on multi-tenant clusters.

## Signed releases

Tagged images (`ghcr.io/whitemug/eviction-guard`) and Helm OCI charts (`ghcr.io/whitemug/charts/eviction-guard`) are signed with Cosign keyless (GitHub Actions OIDC). See [`docs/publishing.md`](docs/publishing.md) for `cosign verify` commands.

## Scanning

- **Go code:** `govulncheck ./...` on every PR.
- **Container image:** Trivy on the image built from `Dockerfile` (`HIGH`/`CRITICAL`, ignore unfixed). The same scan runs on tagged releases (the pushed GHCR digest, before Cosign) and weekly on Mondays so new base-image CVEs show up without a code change. Findings are in the Actions log. Upload to the GitHub **Security** tab (Code scanning) runs only when the repository is **public**; a private repo needs [GitHub Advanced Security](https://docs.github.com/en/get-started/learning-about-github/about-github-advanced-security).
- **Dependencies:** Dependabot watches `go.mod`, the Dockerfile, and GitHub Actions.
