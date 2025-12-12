#!/bin/bash
set -o verbose
set -eu
set -o pipefail

# --- Error Handling Function ---
handle_error() {
    local EXIT_CODE=$?
    local FAILED_COMMAND=$BASH_COMMAND

    printf "{message: \"'%s' failed with exit code %i.\", level: \"ERROR\"}" "$FAILED_COMMAND" "$EXIT_CODE" >&2
}
# Trap the ERR signal to run the function
trap 'handle_error' ERR
# ------------------------------

trap 'ssh -O exit $SSH_HOST 2>/dev/null || true' EXIT # end SSH session upon EXIT

cd /tmp
mkdir -p /root/.ssh
cp "$SSH_WORKSPACE_PATH/id_rsa" /tmp/master_key
chmod 0400 /tmp/master_key
export SSH_HOST="$USER@$HOST"
SSH_MULTIPLEX_OPTS=(-o ControlMaster=auto -o ControlPath=/tmp/ssh-%r@%h:%p)
SSH_OPTS=(-i /tmp/master_key -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null  "${SSH_MULTIPLEX_OPTS[@]}")

USERNAME=u-$(echo "$TASKRUN_NAME$NAMESPACE" | md5sum | cut -b-28)
export USERNAME

# Create script to provision VM
cat >script.sh <<EOF
sudo dnf install podman -y

rm -f "$USERNAME" "$USERNAME".pub

# Retry useradd command as sometimes it hits a "cannot lock /etc/passwd; try again later" error
for i in {10..1}; do
  sudo useradd -m "$USERNAME" -p \$(openssl rand -base64 12)
  if [ \$? -eq 0 ]; then
    echo "{message: \"User $USERNAME added successfully.\", level: \"INFO\"}"
    break
  fi
  ATTEMPTS_REMAINING=\$((i - 1))
  if [ "\$ATTEMPTS_REMAINING" -gt 0 ]; then
    echo "{message: \"User $USERNAME addition attempt failed, retrying \$ATTEMPTS_REMAINING more times...\", level: \"WARNING\"}"
    sleep 1
  else
    echo "{message: \"Failed to add user $USERNAME after 10 total attempts.\", level: \"ERROR\"}"
    exit 1
  fi
done

ssh-keygen -N '' -f "$USERNAME"
sudo su "$USERNAME" -c 'mkdir /home/"$USERNAME"/.ssh'
sudo su "$USERNAME" -c 'mkdir /home/"$USERNAME"/build'
sudo mv "$USERNAME".pub /home/"$USERNAME"/.ssh/authorized_keys
sudo chown "$USERNAME" /home/"$USERNAME"/.ssh/authorized_keys
sudo restorecon -FRvv /home/"$USERNAME"/.ssh
echo "{message: \"Successfully created and configured a new SSH key for $USERNAME.\", level: \"INFO\"}"
EOF

# Add sudo access (if needed) to the script
if [ -n "${SUDO_COMMANDS:-}" ]; then
  cat >>script.sh <<EOF
  echo "$USERNAME ALL=(ALL) NOPASSWD: $SUDO_COMMANDS" | sudo tee -a /etc/sudoers.d/"$USERNAME"
  echo "{message: \"Successfully added sudo access.\", level: \"INFO\"}"
EOF
fi

# Execute provision script on VM
SSH_PROVISION_OUTPUT=$(
    ssh "${SSH_OPTS[@]}" "$SSH_HOST" "bash -s" <script.sh 2>&1
) || {
    # If the command fails, the '||' block executes.
    # Note: Using '||' suppresses set -e for this line.
    echo "{message: \"Remote Provision Output: $SSH_PROVISION_OUTPUT\", level: \"WARNING\"}" >&2
    exit 1
}
echo "$SSH_PROVISION_OUTPUT"
echo "{message: \"Successfully ran provisioning script on VM.\", level: \"INFO\"}"

# Copy/configure remotely-generated SSH key and then delete on VM
ssh "${SSH_OPTS[@]}" "$SSH_HOST" cat "$USERNAME"  >id_rsa
echo "{message: \"Successfully copied remotely-generated SSH key from VM.\", level: \"INFO\"}"
SSH_KEY_RM_OUTPUT=$(
    ssh "${SSH_OPTS[@]}" "$SSH_HOST" rm "$USERNAME" 2>&1
) || {
    # If the command fails, the '||' block executes.
    # Note: Using '||' suppresses set -e for this line.
    echo "{message: \"Remote Key Removal Output: $SSH_KEY_RM_OUTPUT\", level: \"WARNING\"}" >&2
    exit 1
}
echo "{message: \"Successfully deleted SSH key from VM.\", level: \"INFO\"}"

chmod 0400 id_rsa
ENCODED_HOST=$(echo "$USERNAME@$HOST" | base64 -w 0)
DIR=$(echo /home/"$USERNAME" | base64 -w 0)

# Generate a Kubernetes Secret file based on the existence of a certificate
if [ -e "/tls/tls.crt" ]; then
  echo "{message: \"Creating secret file using TLS certificate...\", level: \"INFO\"}"
  KEY=$(cat id_rsa)
  OTP=$(curl --cacert /tls/tls.crt -XPOST -d "$KEY" https://multi-platform-otp-server.multi-platform-controller.svc.cluster.local/store-key | base64 -w 0)
  OTP_SERVER="$(echo https://multi-platform-otp-server.multi-platform-controller.svc.cluster.local/otp | base64 -w 0)"
  echo "$OTP" | base64 -d

  cat >secret.yaml <<EOF
  apiVersion: v1
  data:
    otp-ca: "$(base64 -w 0 < /tls/tls.crt)"
    otp: "$OTP"
    otp-server: "$OTP_SERVER"
    host: "$ENCODED_HOST"
    user-dir: "$DIR"
  kind: Secret
  metadata:
    name: "$SECRET_NAME"
    namespace: "$NAMESPACE"
    labels:
      build.appstudio.redhat.com/multi-platform-secret: "true"
  type: Opaque
EOF

else
  echo "{message: \"Creating secret file based on SSH key alone...\", level: \"INFO\"}"
  KEY=$(base64 -w 0 < id_rsa)

  cat >secret.yaml <<EOF
  apiVersion: v1
  data:
    id_rsa: "$KEY"
    host: "$ENCODED_HOST"
    user-dir: "$DIR"
  kind: Secret
  metadata:
    name: "$SECRET_NAME"
    namespace: "$NAMESPACE"
    labels:
      build.appstudio.redhat.com/multi-platform-secret: "true"
  type: Opaque
EOF

fi

kubectl create -f secret.yaml
echo "{message: \"Successfully created secret.\", level: \"INFO\"}"
