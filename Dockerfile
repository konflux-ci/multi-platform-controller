# Build the manager binary
FROM registry.access.redhat.com/ubi9/go-toolset:9.6-1754467841@sha256:3f552f246b4bd5bdfb4da0812085d381d00d3625769baecaed58c2667d344e5c as builder

# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
COPY pkg/ pkg/
COPY cmd/ cmd/

# Build
RUN GOTOOLCHAIN=auto CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o multi-platform-controller cmd/controller/main.go

# Use ubi-minimal as minimal base image to package the manager binary
# Refer to https://catalog.redhat.com/software/containers/ubi8/ubi-minimal/5c359a62bed8bd75a2c3fba8 for more details
FROM registry.access.redhat.com/ubi9/ubi-minimal:9.6-1753762263@sha256:67fee1a132e8e326434214b3c7ce90b2500b2ad02c9790cc61581feb58d281d5
COPY --from=builder /opt/app-root/src/multi-platform-controller /
USER 65532:65532

ENTRYPOINT ["/multi-platform-controller"]
