# Eviction Guard documentation

Operator-first guides. Start here:

| Doc | What you’ll learn |
|---|---|
| [Overview](overview.md) | What Eviction Guard does and does not do |
| [Install](install.md) | Helm / Kustomize, HA, webhook, verify |
| [Configure](configure.md) | Policies, workloads, signals, backends |
| [How-to](howto.md) | Simulate disruption, eviction gating, troubleshooting |
| [Metrics](metrics.md) | Prometheus gauges/counters |
| [KEDA](keda.md) | Patch ScaledObject vs external + metrics |
| [GitOps](gitops.md) | Argo CD / Flux and capacity field ignore |
| [Signal catalog](signals.md) | Built-in and `customSignals` recipes |
| [Upgrading](../UPGRADING.md) | Breaking changes between releases |
| [Changelog](../CHANGELOG.md) | Release notes |

For integrators and maintainers:

| Doc | Audience |
|---|---|
| [Extension](extension.md) | Other operators / Go plugins |
| [Design](design.md) | Architecture and design rationale |
| [Scale targets](design-scale-targets.md) | Backend catalog design (locked decisions) |
| [Publishing](publishing.md) | Release, GHCR, Cosign |
| [Support](../SUPPORT.md) | Where to ask questions / report bugs |
| [Contributing](../CONTRIBUTING.md) | Dev setup and PR expectations |

Examples live in [`../examples/`](../examples/).
