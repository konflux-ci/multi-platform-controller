#/bin/env bash

set -e -o pipefail

KIND_CLUSTER_NAME=mpc-acceptance-tests
CONTAINER_BUILDER=${CONTAINER_BUILDER:-docker}
CERT_MANAGER_VERSION=${CERT_MANAGER_VERSION:-v1.16.3}

# Calculate script's folder
SCRIPT_DIR=$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )

# Build MPC
${CONTAINER_BUILDER} build "${SCRIPT_DIR}/../.." -t multi-platform-controller:latest
${CONTAINER_BUILDER} build "${SCRIPT_DIR}/../.." -f "${SCRIPT_DIR}/../../Dockerfile.otp" -t multi-platform-otp-server:latest

# (Re)Create Cluster
kind delete cluster --name "${KIND_CLUSTER_NAME}" || true
kind create cluster --name "${KIND_CLUSTER_NAME}"

# Load images
kind load docker-image multi-platform-controller:latest --name "${KIND_CLUSTER_NAME}"
kind load docker-image multi-platform-otp-server:latest --name "${KIND_CLUSTER_NAME}"

# Install Cert Manager
kubectl apply --server-side -f "https://github.com/cert-manager/cert-manager/releases/download/${CERT_MANAGER_VERSION}/cert-manager.yaml"
kubectl wait --for=condition=Available deployment --all -n cert-manager --timeout=300s

# Install Tekton Pipelines
kubectl apply --filename https://storage.googleapis.com/tekton-releases/pipeline/latest/release.yaml 
kubectl wait --for=condition=Available deployment --all --timeout 300s -n tekton-pipelines
kubectl wait --for=condition=Available deployment --all --timeout 300s -n tekton-pipelines

# Install MPC
kustomize build "${SCRIPT_DIR}/../config/" | kubectl apply -f -
kubectl wait --for=condition=Available deployment --all --timeout 300s -n multi-platform-controller
