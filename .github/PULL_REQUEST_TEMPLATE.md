## What

<!-- One or two sentences. Link the issue if there is one. -->

## How to test

- [ ] `make test`
- [ ] `make test-envtest` if reconcilers, CRDs, or status/finalizers changed
- [ ] `make test-kind` if webhook, eviction gating, or e2e paths changed
- [ ] `make test-kind-backends` if backends, HPA/KEDA scaling, or `test/e2e/backends*` changed
- [ ] `make helm-lint` if the chart changed
- [ ] `make generate manifests` committed if `api/` changed
