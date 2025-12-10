#!/bin/bash
set -o verbose
set -eu

# --- Error Handling Function ---
handle_error() {
    local EXIT_CODE=$?
    local FAILED_COMMAND=$BASH_COMMAND

    printf "{message: \"'%s' failed with exit code %i.\", level: \"ERROR\"}" "$FAILED_COMMAND" "$EXIT_CODE" >&2
}
# Trap the ERR signal to run the function
trap 'handle_error' ERR
# ------------------------------

cd /tmp || exit 1
mkdir -p /root/.ssh
cp "$SSH_WORKSPACE_PATH/id_rsa" /tmp/master_key
chmod 0400 /tmp/master_key
export SSH_HOST="$USER@$HOST"
SSH_OPTS=(-i /tmp/master_key -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null)

SSH_UPDATE_OUTPUT=$(
    ssh "${SSH_OPTS[@]}" "$SSH_HOST" "sudo dnf update -y" 2>&1
) || {
    # If the command fails, the `||` block executes.
    # Note: Using `||` suppresses set -e for this line.
    echo "{message: \"Remote Update Output: $SSH_UPDATE_OUTPUT\", level: \"WARNING\"}" >&2
    exit 1
}
echo "{message: \"SSH to $HOST was successful.\", level: \"INFO\"}"

# Create script to clean up dangling users
cat >script.sh <<EOF
threshold=\$(date -d "1 day ago" +%s)
cd /home
for user_dir in u-*; do
  echo "{message: \"Checking '\$user_dir' against threshold...\", level: \"INFO\"}"

  # Skip non-directory entries
  if [[ ! -d "\$user_dir" ]]; then
    continue
  fi

  # Delete the user & directory if the last modification time is older than the threshold
  if [[ \$(stat -c "%Y" "\$user_dir") -lt "\$threshold" ]]; then

    # try to kill all processes associated with \$user_dir
    sudo killall -9 -u "\$user_dir"
    KILL_STATUS=\$?
    if [ \$KILL_STATUS -ne 0 ]; then
      echo "{message: \"Could not kill all processes associated with user '\$user_dir' (exit code \$KILL_STATUS).\", level: \"WARNING\"}"
    fi

    echo "{message: \"Deleting old home directory '\$user_dir' & its associated user...\", level: \"INFO\"}"
    sudo userdel -f -r -Z "\$user_dir"
    # Confirm deletion of user and user directory (0) or just user directory (6)
    DELETE_STATUS=\$? # Capture status immediately
    if [ \$DELETE_STATUS -eq 0 ] || [ \$DELETE_STATUS -eq 6 ]; then
        echo "{message: \"User \$user_dir was successfully deleted.\", level: \"INFO\"}"
    else
        echo "{message: \"User \$user_dir deletion failed (exit code \$DELETE_STATUS).\", level: \"WARNING\"}"
    fi
  fi
done
EOF

# Execute script to prune dangling users remotely
SSH_USER_DEL_OUTPUT=$(
    ssh "${SSH_OPTS[@]}" "$SSH_HOST" "bash -s" <script.sh 2>&1
) || {
    # If the command fails, the `||` block executes.
    # Note: Using `||` suppresses set -e for this line.
    echo "{message: \"Remote User Pruning Output: $SSH_USER_DEL_OUTPUT\", level: \"WARNING\"}" >&2
    exit 1
}
echo "$SSH_USER_DEL_OUTPUT"
echo "{message: \"Remote user pruning on $HOST was successful.\", level: \"INFO\"}"
