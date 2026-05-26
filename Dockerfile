# Build the manager binary
FROM registry.access.redhat.com/ubi9/go-toolset:9.8-1779769584@sha256:a82d974dae02330d0669fb0a5ced2ae498bd1bd708359d61493b9fb0dc0748eb as builder

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
FROM registry.access.redhat.com/ubi9/ubi-minimal:9.8-1779709832@sha256:457cb288730d29e2a59823b3b56341466fa94f3158e399c5ab2c4409fee0c562
COPY --from=builder /opt/app-root/src/multi-platform-controller /
USER 65532:65532

ENTRYPOINT ["/multi-platform-controller"]
