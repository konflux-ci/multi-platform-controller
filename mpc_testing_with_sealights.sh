#!/bin/bash

# making sure anything that fails during the script stops the rest of the script from running
set -euo pipefail

# adding some log-like lines for what's happening here
exec > >(while IFS= read -r line; do echo "$(date +'%Y-%m-%d %H:%M:%S') $line"; done)
exec 2>&1

MODE=""
PULL_REQUEST_NUMBER=""
LATEST_COMMIT_SHA=""
DATE=""

while [[ $# -gt 0 ]]; do
  case $1 in
    --mode)
      MODE="$2"
      shift 2
      ;;
    --pr-number)
      PULL_REQUEST_NUMBER="$2"
      shift 2
      ;;
    --commit-sha)
      LATEST_COMMIT_SHA="$2"
      shift 2
      ;;
    --date)
      DATE="$2"
      shift 2
      ;;
    *)
      echo "Unknown option: $1" >&2
      exit 1
      ;;
  esac
done

# Validate mode and required parameters
if [[ -z "$MODE" ]]; then
  echo "Error: --mode is required" >&2
  exit 1
fi

echo "[Sealights] Downloading Sealights Golang & CLI Agents..."
case $(lscpu | awk '/Architecture:/{print $2}') in
    x86_64) SL_ARCH="linux-amd64";;
    arm) SL_ARCH="linux-arm64";;
          esac
wget -nv -O sealights-go-agent.tar.gz https://agents.sealights.co/slgoagent/latest/slgoagent-$SL_ARCH.tar.gz
wget -nv -O sealights-slcli.tar.gz https://agents.sealights.co/slcli/latest/slcli-$SL_ARCH.tar.gz
tar -xzf ./sealights-go-agent.tar.gz
tar -xzf ./sealights-slcli.tar.gz
rm -f ./sealights-go-agent.tar.gz ./sealights-slcli.tar.gz
./slgoagent -v 2> /dev/null | grep version
./slcli -v 2> /dev/null | grep version
./slcli config init --lang go --token ./sltoken.txt

if [[ "$MODE" == "pull_request" ]]; then
  if [[ -z "$PULL_REQUEST_NUMBER" || -z "$LATEST_COMMIT_SHA" ]]; then
    echo "Error: --pr-number and --commit-sha are required for pull_request_mode" >&2
    exit 1
  fi

  echo "[Sealights] Initiating and configuring SeaLights to scan the pull request branch"
  echo "Latest commit sha: $LATEST_COMMIT_SHA"
  echo "PR Number: $PULL_REQUEST_NUMBER"
  ./slcli config create-pr-bsid --app multi-platform-controller --target-branch "main" --pull-request-number $PULL_REQUEST_NUMBER --latest-commit $LATEST_COMMIT_SHA --repository-url https://github.com/konflux-ci/multi-platform-controller


elif [[ "$MODE" == "merged_main" ]]; then
  if [[ -z "$DATE" ]]; then
    echo "Error: --date is required for merged_main mode" >&2
    exit 1
  fi

  echo "[Sealights] Initiating and configuring SeaLights to scan the merged main branch after pull request was closed"
  ./slcli config create-bsid --app multi-platform-controller --branch main --build multi-platform-controller-main-$DATE

else
  echo "Error: Unsupported mode: $MODE" >&2
  exit 1
fi

echo "[Sealights] Running the SeaLights scan"
./slcli scan --bsid buildSessionId.txt  --path-to-scanner ./slgoagent --workspacepath ./ --scm git --scmBaseUrl https://github.com/konflux-ci/multi-platform-controller --scmVersion “0” --scmProvider github
echo "[Multi-Platform-Controller] - running make build"
make build
echo "[Multi-Platform-Controller] - running tests"
make test
echo "[Sealights] Cleaning up after SeaLights run"
rm sltoken.txt buildSessionId.txt

echo "Script completed successfully."
exit 0
