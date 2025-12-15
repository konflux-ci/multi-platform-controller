#!/bin/bash
set -o verbose
set -eu
set -o pipefail

# --- Error Handling Function ---
# shellcheck disable=SC2317
handle_error() {
    local EXIT_CODE=$?
    local FAILED_COMMAND=$BASH_COMMAND

    printf "{message: \"'%s' failed with exit code %i.\", level: \"ERROR\"}" "$FAILED_COMMAND" "$EXIT_CODE" >&2
}
# Trap the ERR signal to run the function
trap 'handle_error' ERR
# ------------------------------

trap 'ssh -O exit $SSH_HOST 2>/dev/null || true' EXIT

cd /tmp
cp "$SSH_WORKSPACE_PATH/id_rsa" /tmp/master_key
chmod 0400 /tmp/master_key
export SSH_HOST="$USER@$HOST"
SSH_MULTIPLEX_OPTS=(-o ControlMaster=auto -o ControlPath=/tmp/ssh-%r@%h:%p)
SSH_OPTS=(-i /tmp/master_key -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "${SSH_MULTIPLEX_OPTS[@]}")

USERNAME=u-$(echo "$TASKRUN_NAME$NAMESPACE" | md5sum | cut -b-28)
export USERNAME

# Check if the user exists
SSH_USER_CHK_OUTPUT=$(
    ssh "${SSH_OPTS[@]}" "$SSH_HOST" id -u "$USERNAME"
) || {
    # If the command fails, the `||` block executes.
    # Note: Using `||` suppresses set -e for this line.
    exit_code=$?
    if [ $exit_code -eq 1 ]; then
      echo "{message: \"User $USERNAME already deleted, exiting...\", level: \"INFO\"}"
      exit 0
    fi
    echo "{message: \"Remote User Check Output: $SSH_USER_CHK_OUTPUT\", level: \"WARNING\"}" >&2
    exit $exit_code
}
echo "{message: \"User $USERNAME exists, proceeding with cleanup.\", level: \"INFO\"}"

# Kill all processes associated with user
SSH_KILL_OUTPUT=$(
    ssh "${SSH_OPTS[@]}" "$SSH_HOST" sudo killall -9 -u "$USERNAME" 2>&1
) || {
    # If the command fails, the `||` block executes.
    # Note: Using `||` suppresses set -e for this line.
    echo "{message: \"Remote Kill Output: $SSH_KILL_OUTPUT\", level: \"WARNING\"}" >&2
    exit 1
}

for i in {10..1}; do
  if ssh "${SSH_OPTS[@]}" "$SSH_HOST" sudo userdel -f -r -Z "$USERNAME"; then
    echo "{message: \"User $USERNAME successfully deleted.\", level: \"INFO\"}"
    exit 0
  fi
  ATTEMPTS_REMAINING=$((i - 1))
  if [ "$ATTEMPTS_REMAINING" -gt 0 ]; then
    echo "{message: \"User $USERNAME deletion attempt failed, retrying $ATTEMPTS_REMAINING more times...\", level: \"WARNING\"}"
    sleep 1
  fi
done
echo "{message: \"Failed to delete user $USERNAME on $SSH_HOST after 10 total attempts.\", level: \"ERROR\"}"
exit 1
