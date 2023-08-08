#!/bin/bash

#
# Installs controller-gen utility via "go install".
#

set -eu

# controller-gen version
CONTROLLER_GEN_VERSION="${CONTROLLER_GEN_VERSION:-v0.6.2}"

if ! command -v controller-gen &> /dev/null
then
  echo "# Installing controller-gen..."
  pushd "$(mktemp -d)" >/dev/null 2>&1
  go install sigs.k8s.io/controller-tools/cmd/controller-gen@"${CONTROLLER_GEN_VERSION}"
  popd >/dev/null 2>&1
fi

if ! grep -q " $CONTROLLER_GEN_VERSION$" <<<"$(controller-gen --version)"; then
  echo "Current controller-gen version $(controller-gen --version | cut -d' ' -f2) does not match desired version $CONTROLLER_GEN_VERSION."
  echo "In order to update, run:"
  echo "  go install sigs.k8s.io/controller-tools/cmd/controller-gen@${CONTROLLER_GEN_VERSION}"
  echo
  exit 1
fi

# print controller-gen version
controller-gen --version
