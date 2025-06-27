# Build the manager binary
FROM registry.access.redhat.com/ubi9/go-toolset:9.6-1750969886@sha256:3bbd87d77ea93742bd71a5275a31ec4a7693454ab80492c6a7d28ce6eef35378 as builder

# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
COPY pkg/ pkg/
COPY cmd/ cmd/

# Build
RUN GOTOOLCHAIN=auto CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o multi-platform-controller cmd/controller/main.go

# Use ubi-minimal as minimal base image to package the manager binary
# Refer to https://catalog.redhat.com/software/containers/ubi8/ubi-minimal/5c359a62bed8bd75a2c3fba8 for more details
FROM registry.access.redhat.com/ubi9/ubi-minimal:9.6-1749489516@sha256:f172b3082a3d1bbe789a1057f03883c1113243564f01cd3020e27548b911d3f8
COPY --from=builder /opt/app-root/src/multi-platform-controller /
USER 65532:65532

ENTRYPOINT ["/multi-platform-controller"]
