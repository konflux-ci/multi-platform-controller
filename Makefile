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

test: fmt vet ## Run tests.
	go test -v ./pkg/... -coverprofile cover.out

build:
	go build -o out/multi-platform-controller cmd/controller/main.go
	env GOOS=linux GOARCH=amd64 go build -mod=vendor -o out/multi-platform-controller ./cmd/controller

build-otp:
	env GOOS=linux GOARCH=amd64 go build -mod=vendor -o out/otp-server ./cmd/otp

clean:
	rm -rf out

dev-image:
	docker build . -t quay.io/$(QUAY_USERNAME)/multi-platform-controller:dev
	docker push quay.io/$(QUAY_USERNAME)/multi-platform-controller:dev
	docker build . -f Dockerfile.otp -t quay.io/$(QUAY_USERNAME)/multi-platform-otp:dev
	docker push quay.io/$(QUAY_USERNAME)/multi-platform-otp:dev

dev: dev-image
	./deploy/development.sh

dev-minikube: dev
	./deploy/minikube-development.sh

ENVTEST = $(shell pwd)/bin/setup-envtest
envtest: ## Download envtest-setup locally if necessary.
	$(call go-get-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest@latest)

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
