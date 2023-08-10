# Build the manager binary
FROM registry.access.redhat.com/ubi9/go-toolset:1.19 as builder

# Copy the Go Modules manifests
COPY go.mod go.mod
COPY vendor/ vendor/
COPY pkg/ pkg/
COPY cmd/ cmd/

# Build
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o multi-arch-controller cmd/controller/main.go

# Use ubi-minimal as minimal base image to package the manager binary
# Refer to https://catalog.redhat.com/software/containers/ubi8/ubi-minimal/5c359a62bed8bd75a2c3fba8 for more details
FROM registry.access.redhat.com/ubi9/ubi-minimal:9.2
COPY --from=builder /opt/app-root/src/multi-arch-controller /
USER 65532:65532

ENTRYPOINT ["/multi-arch-controller"]
