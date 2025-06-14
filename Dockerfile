# Build the manager binary
FROM registry.access.redhat.com/ubi9/go-toolset:9.6-1749636489@sha256:2a88121395084eaa575e5758b903fffb43dbf9d9586b2878e51678f63235b587 as builder

# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
COPY pkg/ pkg/
COPY cmd/ cmd/

# Build
RUN GOTOOLCHAIN=auto CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o multi-platform-controller cmd/controller/main.go

# Use ubi-minimal as minimal base image to package the manager binary
# Refer to https://catalog.redhat.com/software/containers/ubi8/ubi-minimal/5c359a62bed8bd75a2c3fba8 for more details
FROM registry.access.redhat.com/ubi9/ubi-minimal:9.5-1742914212@sha256:ac61c96b93894b9169221e87718733354dd3765dd4a62b275893c7ff0d876869
COPY --from=builder /opt/app-root/src/multi-platform-controller /
USER 65532:65532

ENTRYPOINT ["/multi-platform-controller"]
