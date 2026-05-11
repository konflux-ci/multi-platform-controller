# Build the manager binary
FROM registry.access.redhat.com/ubi9/go-toolset:9.7-1778171507@sha256:f9c8537423d96da6c0e704a7d40a1bd5401c4287a05c7f7f196550a5a375a6b6 as builder

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
FROM registry.access.redhat.com/ubi9/ubi-minimal:9.7-1778461551@sha256:fe9e574f04371b333ed4e21d30d984f6b7fcd1046e579f5ddab4816c0c8e231d
COPY --from=builder /opt/app-root/src/multi-platform-controller /
USER 65532:65532

ENTRYPOINT ["/multi-platform-controller"]
