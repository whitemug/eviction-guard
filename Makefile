# Image URL to use all building/pushing image targets
IMG ?= ghcr.io/whitemug/eviction-guard:latest
# Needs Go 1.27+ (see go.mod). Override if it is not on PATH:
#   make test GO=/usr/local/go/bin/go
GO ?= go

.PHONY: all
all: test

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

.PHONY: fmt
fmt: ## go fmt
	$(GO) fmt ./...

.PHONY: vet
vet: ## go vet
	$(GO) vet ./...

.PHONY: generate
generate: controller-gen ## Generate DeepCopy methods
	$(CONTROLLER_GEN) object:headerFile="scripts/boilerplate.go.txt" paths="./api/..."

.PHONY: manifests
manifests: controller-gen ## Generate CRDs and RBAC
	$(CONTROLLER_GEN) rbac:roleName=eviction-guard-manager-role crd paths="./..." output:crd:artifacts:config=config/crd/bases output:rbac:artifacts:config=config/rbac
	$(MAKE) helm-crds

.PHONY: helm-crds
helm-crds: ## Copy generated CRDs into the Helm chart
	mkdir -p charts/eviction-guard/crds
	cp config/crd/bases/*.yaml charts/eviction-guard/crds/

.PHONY: test
test: generate manifests fmt vet ## Run unit tests (fake client; no envtest)
	$(GO) test ./... -coverprofile cover.out

ENVTEST ?= $(LOCALBIN)/setup-envtest
ENVTEST_K8S_VERSION ?= 1.32

.PHONY: setup-envtest
setup-envtest: $(ENVTEST) ## Install setup-envtest and download kube-apiserver/etcd
	$(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path >/dev/null
$(ENVTEST): | localbin
	GOBIN=$(LOCALBIN) $(GO) install sigs.k8s.io/controller-runtime/tools/setup-envtest@release-0.20

.PHONY: test-envtest
test-envtest: generate manifests setup-envtest ## Run envtest against a real API server
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" \
		$(GO) test ./internal/controller/envtest -tags=envtest -count=1 -timeout 5m -v

.PHONY: test-kind
test-kind: ## Kind cluster: taint → scale-up → drain → cooldown → scale-back
	chmod +x test/e2e/kind.sh
	test/e2e/kind.sh

.PHONY: helm-lint
helm-lint: ## helm lint + helm template (no cluster)
	helm lint charts/eviction-guard
	helm template eviction-guard charts/eviction-guard --namespace eviction-guard-system >/dev/null

GOLANGCI_LINT ?= $(LOCALBIN)/golangci-lint
GOLANGCI_LINT_VERSION ?= v2.13.2

.PHONY: lint
lint: $(GOLANGCI_LINT) ## golangci-lint
	$(GOLANGCI_LINT) run

$(GOLANGCI_LINT): | localbin
	GOBIN=$(LOCALBIN) $(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

.PHONY: govulncheck
govulncheck: ## Scan Go modules for known vulnerabilities
	$(GO) run golang.org/x/vuln/cmd/govulncheck@latest ./...

.PHONY: build
build: generate fmt vet ## Build manager binary
	$(GO) build -o bin/manager cmd/main.go

.PHONY: docker-build
docker-build: ## Build container image
	docker build -t $(IMG) .

.PHONY: docker-push
docker-push: ## Push container image
	docker push $(IMG)

.PHONY: install
install: manifests ## Install CRDs into the current cluster
	kubectl apply -f config/crd/bases

.PHONY: uninstall
uninstall: ## Remove CRDs from the current cluster
	kubectl delete -f config/crd/bases --ignore-not-found

.PHONY: deploy
deploy: manifests ## Deploy controller with kustomize
	cd config/manager && $(KUSTOMIZE) edit set image controller=$(IMG)
	$(KUSTOMIZE) build config/default | kubectl apply -f -

.PHONY: undeploy
undeploy: ## Remove controller from the cluster
	$(KUSTOMIZE) build config/default | kubectl delete --ignore-not-found -f -

CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
KUSTOMIZE ?= $(LOCALBIN)/kustomize
LOCALBIN ?= $(shell pwd)/bin

.PHONY: localbin
localbin:
	mkdir -p $(LOCALBIN)

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN)
$(CONTROLLER_GEN): | localbin
	GOBIN=$(LOCALBIN) $(GO) install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.17.2

.PHONY: kustomize
kustomize: $(KUSTOMIZE)
$(KUSTOMIZE): | localbin
	GOBIN=$(LOCALBIN) $(GO) install sigs.k8s.io/kustomize/kustomize/v5@v5.6.0
