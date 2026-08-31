# Contributing

Eviction Guard is intended as a small, composable Kubernetes controller. Changes that help *other operators* integrate (CRD fields, `pkg/` APIs, node filters, backends) are especially welcome.

## Development

Requires **Go 1.27.0** on `PATH` (`go version` should print `go1.27`). The module pins this in `go.mod` (`go 1.27.0`); CI reads that file and the Dockerfile uses `golang:1.27.0`. An older local `go` (1.18–1.20) fails with `invalid go version '1.23.0': must match format 1.23` — that is the *old binary* rejecting a modern `go.mod`, not this project using 1.23. Override with `make test GO=/path/to/go1.27/bin/go`.

```bash
make test          # generate, manifests, fmt, vet, unit tests (fake client)
make test-envtest  # both reconcilers against kube-apiserver (downloads envtest assets)
make test-kind     # kind: taint → scale-up → drain → SpareReady → cooldown → scale-back
make helm-lint     # helm lint + helm template (no cluster)
make lint          # golangci-lint
make govulncheck   # known-vulnerability scan
make build         # bin/manager
```

CI runs the same gates on every PR (`test` job), then `make test-kind` (`e2e` job). A parallel `image` job builds the container and runs Trivy (`HIGH`/`CRITICAL`). Tagged `v*` releases push the image and Helm OCI chart to GHCR, Trivy-scan the digest, and Cosign-sign both.

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

`api/v1alpha1` is **alpha**. We may change fields before v1beta1. Do not break:

- Label / annotation constants in `api/v1alpha1/labels.go`
- `pkg/plugin`, `pkg/backends.Backend`, `pkg/signals.Detector`, `pkg/filters.Filter`

New backends and detectors should be registered, not hardcoded in the reconcilers.

After changing `api/`, run `make generate manifests` and commit the generated CRDs and `zz_generated.deepcopy.go`.

Please follow the [Code of Conduct](CODE_OF_CONDUCT.md).

## Tests

Add unit tests next to the package you change (`pkg/filters`, `pkg/signals`, `internal/controller`). The default `make test` suite uses the fake client.

`make test-envtest` starts kube-apiserver via controller-runtime envtest and runs the scale-up → SpareReady → cooldown → scale-back loop in `internal/controller/envtest`. Use that for CRD, status, and finalizer behavior.

## DCO / license

By contributing you agree the work is MIT-licensed, same as this repository.
