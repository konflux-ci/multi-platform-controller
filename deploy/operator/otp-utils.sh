#!/bin/bash
# Sourced by provisioning scripts — inherits set -eu, pipefail, and ERR trap from parent.

# curl_otp_store_key runs curl with retry logic for transient errors.
# Usage: result=$(curl_otp_store_key --fail --connect-timeout 5 ... URL)
curl_otp_store_key() {
  local otp_fail_prefix='{message: "Failed to store SSH key in OTP server'
  local otp_fail_suffix='. Please, retry build in a few minutes, and if problem persists, please report it as an MPC bug.", level: "ERROR"}'
  local otp_raw=""
  for i in {3..1}; do
    otp_raw=$(curl "$@") && break
    rc=$?
    case "$rc" in
      6|7|22|28|35|56) ;; # transient: DNS, conn refused, HTTP 5xx, timeout, TLS, recv failure
      *) echo "${otp_fail_prefix} (curl exit ${rc})${otp_fail_suffix}" >&2
         exit 1 ;;
    esac
    ATTEMPTS_REMAINING=$((i - 1))
    if [ "$ATTEMPTS_REMAINING" -gt 0 ]; then
      echo "{message: \"OTP server /store-key call failed (curl exit $rc), retrying $ATTEMPTS_REMAINING more times...\", level: \"WARNING\"}" >&2
      sleep 1
    else
      echo "${otp_fail_prefix} after 3 attempts${otp_fail_suffix}" >&2
      exit 1
    fi
  done
  if [ -z "$otp_raw" ]; then
    echo "{message: \"OTP server returned an empty token. Please, retry build in a few minutes, and if problem persists, please report it as an MPC bug.\", level: \"ERROR\"}" >&2
    exit 1
  fi
  printf '%s' "$otp_raw"
}
