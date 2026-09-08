# Contributing

Eviction Guard is intended as a small, composable Kubernetes controller. Changes that help *other operators* integrate (CRD fields, `pkg/` APIs, node filters, backends) are especially welcome.

Look for issues labeled [`good first issue`](https://github.com/whitemug/eviction-guard/issues?q=is%3Aissue+is%3Aopen+label%3A%22good+first+issue%22). A typical first PR is an example policy, a catalog entry, or a unit test next to `pkg/signals` / `pkg/filters` — not a new CRD field.

Questions that are not bugs belong in [Discussions](https://github.com/whitemug/eviction-guard/discussions) or see [SUPPORT.md](SUPPORT.md).

## Development

Requires **Go 1.27.1** on `PATH` (`go version` should print `go1.27`). The module pins this in `go.mod` (`go 1.27.1`); CI reads that file and the Dockerfile uses `golang:1.27.1`. An older local `go` (1.18–1.20) fails with `invalid go version '1.23.0': must match format 1.23` — that is the *old binary* rejecting a modern `go.mod`, not this project using 1.23. Override with `make test GO=/path/to/go1.27/bin/go`.

```bash
make test          # generate, manifests, fmt, vet, unit tests (fake client)
make test-envtest  # both reconcilers against kube-apiserver (downloads envtest assets)
make test-kind     # kind: taint → eviction deny → scale → SpareReady → eviction allow → scale-back
                   # ARGS='--verbose' or VERBOSE=1 for Policy/Window dumps; KEEP=1 to retain cluster
make helm-lint     # helm lint + helm template (no cluster)
make lint          # golangci-lint
make govulncheck   # known-vulnerability scan
make build         # bin/manager
```

CI runs the same gates on every PR (`test` job), then `make test-kind` (`e2e` job). A parallel `image` job builds the container and runs Trivy (`HIGH`/`CRITICAL`). Tagged `v*` releases push the image and Helm OCI chart to GHCR, Trivy-scan the digest, Cosign-sign both, and create a GitHub Release from `CHANGELOG.md`.

Run against a cluster (kind / k3d is enough):

```bash
make docker-build IMG=eviction-guard:dev
make install
kubectl apply -k config/default
kubectl apply -f examples/policy-spot.yaml
kubectl apply -f examples/workload.yaml
```

Simulate disruption:

```bash
kubectl taint node <node> karpenter.sh/disrupted=:NoSchedule
```

## API compatibility

`api/v1alpha1` is **alpha**. We may change CRD fields before v1beta1. Do not break without a major chart/module bump:

- Label / annotation **strings** in `api/v1alpha1/labels.go`
- `pkg/plugin.RegisterSignal`, `pkg/signals.Detector`, `pkg/filters.Filter` / `FromSpec`
- `pkg/evictgate`, `pkg/policyown`
- Catalog helpers in `pkg/backends` (`ParseBindings`, `BindingsForWorkload`, `ResolveCatalog`, `Current` / `ScaleUp` / `ScaleDown`, `RestoreMinReplicas`)

Prometheus metric **names** (`evg_*`) are an observability contract; the `pkg/metrics` Go package is not.

Node coverage is configured via `EvictionGuardPolicy.spec.nodeFilter` (and `customSignals`). Capacity targets use `spec.backends` — there is no Go filter/backend registry for those.

After changing `api/`, run `make generate manifests` and commit the generated CRDs and `zz_generated.deepcopy.go`.

Please follow the [Code of Conduct](CODE_OF_CONDUCT.md).

## Tests

Add unit tests next to the package you change (`pkg/filters`, `pkg/signals`, `pkg/evictgate`, `internal/controller`). The default `make test` suite uses the fake client.

`make test-envtest` starts kube-apiserver via controller-runtime envtest and runs the scale-up → SpareReady → cooldown → scale-back loop in `internal/controller/envtest`. Use that for CRD, status, and finalizer behavior.

Docs for operators live under [`docs/`](docs/README.md) (overview, install, configure, how-to). Keep them aligned when behavior changes. Migration notes: [UPGRADING.md](UPGRADING.md).

## License

By contributing you agree the work is MIT-licensed, same as this repository.
