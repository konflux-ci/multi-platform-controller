# Build the manager binary
FROM registry.access.redhat.com/ubi9/go-toolset:9.8-1784751462@sha256:5f5c97d7e6d917b8328321bcf2c9d5700de65b72d434ecdbbba6f35aaebaad40 as builder

ARG ENABLE_COVERAGE=false

# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
COPY pkg/ pkg/
COPY cmd/ cmd/

# Build with or without coverage instrumentation
RUN if [ "$ENABLE_COVERAGE" = "true" ]; then \
        echo "Building with coverage instrumentation..."; \
        GOTOOLCHAIN=auto CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -cover -covermode=atomic -tags=coverage -o multi-platform-controller ./cmd/controller; \
    else \
        echo "Building production binary..."; \
        GOTOOLCHAIN=auto CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o multi-platform-controller cmd/controller/main.go; \
    fi

# Use ubi-minimal as minimal base image to package the manager binary
# Refer to https://catalog.redhat.com/software/containers/ubi8/ubi-minimal/5c359a62bed8bd75a2c3fba8 for more details
FROM registry.access.redhat.com/ubi9/ubi-minimal:9.8-1780378819@sha256:ae09ecc3d754bc1726cbda3e2599cc7839e09fe1cc547ce173cf669b645be3cc
COPY --from=builder /opt/app-root/src/multi-platform-controller /
USER 65532:65532

ENTRYPOINT ["/multi-platform-controller"]
