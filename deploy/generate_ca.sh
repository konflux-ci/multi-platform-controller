#!/bin/sh

#  This script generates the certificates needed for the otp

set -e


THIS_DIR="$(dirname "$(realpath "$0")")"
TEMP_DIR="${THIS_DIR}/../.tmp"

GENCERTS_DIR="${GENCERTS_DIR:-"${TEMP_DIR}/k8s/certs"}"

echo "Generating CA bundle"
mkdir -p "${GENCERTS_DIR}"

openssl genrsa -out ${GENCERTS_DIR}/ca.key 2048
openssl req -x509 -new -nodes -key ${GENCERTS_DIR}/ca.key -subj "/CN=otp-service.mpc.svc" -days 365 -out ${GENCERTS_DIR}/ca.crt

# verify created
cat ${GENCERTS_DIR}/ca.crt >/dev/null
if [ $? -eq 0 ]; then
  echo "CA bundle created"
fi