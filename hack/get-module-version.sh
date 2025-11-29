#!/bin/bash

# Script to extract Kueue version from go.mod file
# Usage: ./scripts/get-module-version.sh <module-name>

set -e
MODULE_NAME=${1:?Module name is required}
# Extract Kueue version from go.mod
VERSION=$(grep "$MODULE_NAME" go.mod | awk '{print $2}')

if [ -z "$VERSION" ]; then
    echo "Error: Could not find $MODULE_NAME in go.mod" >&2
    exit 1
fi

echo "$VERSION"
