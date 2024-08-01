SHELL := /bin/bash

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

.EXPORT_ALL_VARIABLES:

default: build

fmt: ## Run go fmt against code.
	go fmt ./cmd/... ./pkg/...

vet: ## Run go vet against code.
	go vet ./cmd/... ./pkg/...

manifests: controller-gen
	$(CONTROLLER_GEN) rbac:roleName=multi-platform-controller paths="./..." output:rbac:dir=deploy/operator

.PHONY: test
test: fmt vet ## Run tests.
	go test -v ./pkg/... -coverprofile cover.out

.PHONY: build
build: fmt vet clean manifests
	go build -o out/multi-platform-controller cmd/controller/main.go
	env GOOS=linux GOARCH=amd64 go build -mod=vendor -o out/multi-platform-controller ./cmd/controller

.PHONY: build-otp
build-otp:
	env GOOS=linux GOARCH=amd64 go build -mod=vendor -o out/otp-server ./cmd/otp

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
dev: dev-image
	./deploy/development.sh

dev-minikube: dev
	./deploy/minikube-development.sh

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

.PHONY: envtest
envtest: ## Download envtest-setup locally if necessary.
	$(call go-get-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest@latest)

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download <controller-gen locally if necessary. If wrong version is installed, it will be overwritten.
$(CONTROLLER_GEN): $(LOCALBIN)
	test -s $(CONTROLLER_GEN) && $(CONTROLLER_GEN) --version | grep -q $(CONTROLLER_TOOLS_VERSION) || \
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION)


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
