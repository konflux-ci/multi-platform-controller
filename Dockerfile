# Build the manager binary
FROM registry.access.redhat.com/ubi9/go-toolset:1.20.12-3@sha256:16d035e4a669b76ca73e04fe4ea3583cb3a7753842c3b9d954758f6726c3ba64 as builder

# Copy the Go Modules manifests
COPY go.mod go.mod
COPY vendor/ vendor/
COPY pkg/ pkg/
COPY cmd/ cmd/

# Build
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o multi-platform-controller cmd/controller/main.go

# Use ubi-minimal as minimal base image to package the manager binary
# Refer to https://catalog.redhat.com/software/containers/ubi8/ubi-minimal/5c359a62bed8bd75a2c3fba8 for more details
FROM registry.access.redhat.com/ubi9/ubi-minimal:9.3-1552@sha256:582e18f13291d7c686ec4e6e92d20b24c62ae0fc72767c46f30a69b1a6198055
COPY --from=builder /opt/app-root/src/multi-platform-controller /
USER 65532:65532

ENTRYPOINT ["/multi-platform-controller"]
