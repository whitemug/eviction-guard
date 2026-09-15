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

Eviction Guard is a cluster-scoped operator. It can patch Deployments and HPAs (and other catalog backends), and (with the default webhook) **deny `pods/eviction`** for opted-in pods until spare capacity is Ready. Treat `EvictionGuardPolicy` create/update as a privileged action; restrict it with RBAC the same way you restrict HPA or ClusterAutoscaler objects. On shared clusters, deny tenant Policy create.

Cross-namespace capacity mutation is **supported and intentional**: a protected Deployment may set `eviction-guard.io/scale-backend` to `key=ns/name` (for example `hpa=platform/web-hpa`) so Eviction Guard patches capacity in another namespace. There is no same-namespace deny in the operator. Isolation is an ops / RBAC concern — who may create Policies, who may annotate Deployments, and how ClusterRole patch rights are scoped. See [docs/configure.md](docs/configure.md).

With `webhook.evictionFailurePolicy: Fail` (Helm default), a webhook outage rejects **every** `pods/eviction` CREATE cluster-wide until the webhook recovers — not only opted-in pods. That is intentional; do not set `Ignore` unless you accept cold drains during outages. Deployment annotation checks default to `Ignore`. See [docs/howto.md](docs/howto.md) and [docs/install.md](docs/install.md).

The manager metrics endpoint (default `:8080`) is plaintext and unauthenticated. The Helm chart ships a metrics ClusterIP Service (`metrics.service.enabled`, default true). `networkPolicy.enabled` (default true) only narrows ingress to the manager's own health/metrics/webhook ports — it does not restrict *who* can reach them, so the metrics endpoint remains scrapeable from any in-cluster pod. On multi-tenant clusters, bind metrics to localhost and scrape via a sidecar, or fork `networkpolicy.yaml` with a scoped `from:`, if you need real isolation.

## Webhook TLS expiry

Helm self-signed webhook certs live in Secret `eviction-guard-webhook-certs` and are **reused** across upgrades. They expire after `webhook.certDurationDays` (default 365). Monitor Secret / Certificate notAfter, or calendar a rotation before expiry:

```bash
kubectl delete secret -n eviction-guard-system eviction-guard-webhook-certs
helm upgrade --install eviction-guard ...
```

Prefer `webhook.certManager.enabled=true` (or the Kustomize `config/certmanager` path) for automated rotation. See [UPGRADING.md](UPGRADING.md).

## Signed releases

Tagged images (`ghcr.io/whitemug/eviction-guard`) and Helm OCI charts (`ghcr.io/whitemug/charts/eviction-guard`) are signed with Cosign keyless (GitHub Actions OIDC). See [`docs/publishing.md`](docs/publishing.md) for `cosign verify` commands.

## Scanning

- **Go code:** `govulncheck ./...` on every PR.
- **Container image:** Trivy on the image built from `Dockerfile` (`HIGH`/`CRITICAL`, ignore unfixed). The same scan runs on tagged releases (the pushed GHCR digest, before Cosign) and weekly on Mondays so new base-image CVEs show up without a code change. Findings are in the Actions log. Upload to the GitHub **Security** tab (Code scanning) runs only when the repository is **public**; a private repo needs [GitHub Advanced Security](https://docs.github.com/en/get-started/learning-about-github/about-github-advanced-security).
- **Dependencies:** Dependabot watches `go.mod`, the Dockerfile, and GitHub Actions.
