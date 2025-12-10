SHELL := /bin/bash

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

GOLANGCI_LINT ?= go run -modfile $(shell realpath ./hack/tools/golang-ci/go.mod) github.com/golangci/golangci-lint/v2/cmd/golangci-lint
GINKGO ?= go run github.com/onsi/ginkgo/v2/ginkgo

.EXPORT_ALL_VARIABLES:

default: build

fmt: ## Run go fmt against code.
	$(GOLANGCI_LINT) fmt $(FMT_ARGS) ./...

vet: ## Run go vet against code.
	go vet ./cmd/... ./pkg/...

manifests: controller-gen
	$(CONTROLLER_GEN) rbac:roleName=manager-role paths="./..." output:rbac:dir=deploy/operator/rbac

.PHONY: test
test: fmt vet ## Run tests.
	$(GINKGO) --race -p --github-output -coverprofile cover.out -covermode atomic --json-report=test-report.json -v ././cmd/... ././pkg/...

.PHONY: lint
lint: lint-go

.PHONY: lint-go
lint-go:
	$(GOLANGCI_LINT) run $(LINT_ARGS) ./...

.PHONY: build
build: fmt vet clean manifests
	go build -o out/multi-platform-controller cmd/controller/main.go
	env GOOS=linux GOARCH=amd64 go build -o out/multi-platform-controller ./cmd/controller

.PHONY: build-otp
build-otp:
	env GOOS=linux GOARCH=amd64 go build -o out/otp-server ./cmd/otp

.PHONY: build-devsetup
build-devsetup:
	go build -o out/devsetup ./cmd/devsetup

.PHONY: clean
clean:
	rm -rf out

.PHONY: dev-image
dev-image: build build-otp
	docker build . -t quay.io/$(QUAY_USERNAME)/multi-platform-controller:dev
	docker push quay.io/$(QUAY_USERNAME)/multi-platform-controller:dev
	docker build . -f Dockerfile.otp -t quay.io/$(QUAY_USERNAME)/multi-platform-otp:dev
	docker push quay.io/$(QUAY_USERNAME)/multi-platform-otp:dev

.PHONY: dev
dev: dev-image build-devsetup
	./out/devsetup deploy

.PHONY: dev-minikube
dev-minikube: dev-image
	./deploy/generate_ca.sh
	./deploy/minikube-development.sh

.PHONY: deploy
deploy: build-devsetup
	./out/devsetup deploy

## Tool Versions
KUSTOMIZE_VERSION ?= v4.4.1
CONTROLLER_TOOLS_VERSION ?= v0.11.1

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

## Tool Binaries
KUSTOMIZE ?= $(LOCALBIN)/kustomize
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest
CONTAINER_TOOL ?= podman
TEKTON_VERSION ?= $(shell ./hack/get-module-version.sh github.com/tektoncd/pipeline)
CERT_MANAGER_VERSION ?= v1.19.2
KUBECTL ?= kubectl
QUAY_USERNAME ?= konflux-ci
CONTROLLER_IMAGE=quay.io/$(QUAY_USERNAME)/multi-platform-controller:dev
OTP_IMAGE=quay.io/$(QUAY_USERNAME)/multi-platform-otp:dev

KIND_CLUSTER ?= kind

.PHONY: envtest
envtest: ## Download envtest-setup locally if necessary.
	$(call go-get-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest@latest)

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download <controller-gen locally if necessary. If wrong version is installed, it will be overwritten.
$(CONTROLLER_GEN): $(LOCALBIN)
	test -s $(CONTROLLER_GEN) && $(CONTROLLER_GEN) --version | grep -q $(CONTROLLER_TOOLS_VERSION) || \
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION)

.PHONY: tekton
tekton:
	$(KUBECTL) apply --server-side -f https://storage.googleapis.com/tekton-releases/pipeline/previous/$(TEKTON_VERSION)/release.yaml
	$(KUBECTL) wait --for=condition=Available deployment --all -n tekton-pipelines --timeout=300s

.PHONY: cert-manager
cert-manager:
	$(KUBECTL) apply --server-side -f https://github.com/cert-manager/cert-manager/releases/download/$(CERT_MANAGER_VERSION)/cert-manager.yaml
	$(KUBECTL) wait --for=condition=Available deployment --all -n cert-manager --timeout=300s

.PHONY: build-controller-image
build-controller-image:
	$(CONTAINER_TOOL) build -t $(CONTROLLER_IMAGE) -f Dockerfile .

.PHONY: build-otp-image
build-otp-image:
	$(CONTAINER_TOOL) build -t $(OTP_IMAGE) -f Dockerfile.otp .

.PHONY: load-image
load-image: build-controller-image build-otp-image
	dir=$$(mktemp -d) && \
	$(CONTAINER_TOOL) save $(CONTROLLER_IMAGE) -o $${dir}/multi-platform-controller.tar && \
	$(CONTAINER_TOOL) save $(OTP_IMAGE) -o $${dir}/otp-server.tar && \
	kind load image-archive -n $(KIND_CLUSTER) $${dir}/multi-platform-controller.tar && \
	kind load image-archive -n $(KIND_CLUSTER) $${dir}/otp-server.tar && \
	rm -r $${dir}

.PHONY: test-e2e
test-e2e: test-e2e-deployment test-e2e-taskrun ## Run all e2e tests: deployment tests first, then taskrun tests in parallel

.PHONY: test-e2e-deployment
test-e2e-deployment: ## Run deployment validation e2e tests
	$(GINKGO) --github-output -coverprofile cover.out -covermode atomic -v ./test/e2e/deployment/

.PHONY: test-e2e-taskrun
test-e2e-taskrun: ## Run TaskRun execution e2e tests in parallel
	$(GINKGO) --github-output -coverprofile cover.out -covermode atomic -v -procs=4 ./test/e2e/taskrun/

# go-get-tool will 'go get' any package $2 and install it to $1.
PROJECT_DIR := $(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))
define go-get-tool
@[ -f $(1) ] || { \
set -e ;\
TMP_DIR=$$(mktemp -d) ;\
cd $$TMP_DIR ;\
go mod init tmp ;\
echo "Downloading $(2)" ;\
GOBIN=$(PROJECT_DIR)/bin go get $(2) ;\
rm -rf $$TMP_DIR ;\
}
endef


#test
