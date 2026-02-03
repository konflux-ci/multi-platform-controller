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
export PLATFORM="${RAW_PLATFORM//-/_}"
SSH_MULTIPLEX_OPTS=(-o ControlMaster=auto -o ControlPath=/tmp/ssh-%r@%h:%p)
SSH_OPTS=(-i /tmp/master_key -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null  "${SSH_MULTIPLEX_OPTS[@]}")

USERNAME=u-$(echo "$TASKRUN_NAME$NAMESPACE" | md5sum | cut -b-28)
export USERNAME

echo "$SSH_WORKSPACE_PATH"
echo "$USERNAME"
ls -la /tmp
ls -la $SSH_WORKSPACE_PATH
ls -la /tmp/master_key
cat /tmp/master_key
ls -la /tmp/master_key
sleep 600

# Create script to provision VM
cat >script.sh <<EOF
sudo dnf install podman -y

if command -v otelcol-contrib >/dev/null 2>&1; then
  echo "Found Opentelemetry, re-apply config.."
  sudo systemctl restart otelcol-contrib
else
  if [[ -f /etc/otelcol-contrib/config_mpc.yaml ]]; then
    PKG="otelcol-contrib_0.140.0_${PLATFORM}.rpm"
    URL="https://github.com/open-telemetry/opentelemetry-collector-releases/releases/download/v0.140.0/\$PKG"
    echo "Downloading: \$URL"
    curl -LO "\$URL" || exit 1
    sudo rpm -ivh "\$PKG" || exit 1
    # Ensure config dir exists
    sudo install -d /etc/otelcol-contrib

    # Patch config file name (overwrite)
    echo 'OTELCOL_OPTIONS="--config=/etc/otelcol-contrib/config_mpc.yaml"' \
      | sudo tee /etc/otelcol-contrib/otelcol-contrib.conf >/dev/null

    # Add user to groups BEFORE restart
    sudo usermod -aG adm,systemd-journal otelcol-contrib

    # Set ACLs to read the audit log
    sudo setfacl -m u:otelcol-contrib:r /var/log/audit/audit.log
    sudo setfacl -m u:otelcol-contrib:rx /var/log/audit

    sudo systemctl daemon-reload
    sudo systemctl restart otelcol-contrib
  else
    echo "Opentelemetry config not found, skipping installation."
  fi
fi

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

# Set resource limits for this user to prevent fork bombs
sudo tee /etc/security/limits.d/user-$USERNAME.conf >/dev/null <<LIMITS_EOF
$USERNAME   soft   nproc   2048
$USERNAME   hard   nproc   4096
$USERNAME   soft   cpu     28800      # 8 hours
$USERNAME   hard   cpu     43200      # 12 hours
LIMITS_EOF

# Ensure PAM enforces these limits
PAM_FILE="/etc/pam.d/system-auth"

# Add pam_limits.so if not already present
if ! grep -q "pam_limits.so" "\$PAM_FILE"; then
  echo "session required pam_limits.so" | sudo tee -a "\$PAM_FILE" >/dev/null
  echo "{message: \"PAM limits enabled in \$PAM_FILE.\", level: \"INFO\"}"
else
  echo "{message: \"PAM limits already configured in \$PAM_FILE.\", level: \"INFO\"}"
fi

SSH_PAM_FILE="/etc/pam.d/sshd"
if ! grep -q "pam_limits.so" "\$SSH_PAM_FILE"; then
  echo "session required pam_limits.so" | sudo tee -a "\$SSH_PAM_FILE" >/dev/null
  echo "{message: \"PAM limits enabled in \$SSH_PAM_FILE.\", level: \"INFO\"}"
else
  echo "{message: \"PAM limits already configured in \$SSH_PAM_FILE.\", level: \"INFO\"}"
fi

# Get the UID for systemd slice configuration
USER_UID=\$(id -u $USERNAME)

# Calculate CPU quota dynamically based on available CPUs
# Allow user to use 90% of total CPU capacity, leaving 10% for system
TOTAL_CPUS=\$(nproc)
CPU_QUOTA_PERCENT=\$((TOTAL_CPUS * 90))

# Configure systemd user slice limits for additional protection
sudo mkdir -p "/etc/systemd/system/user-\${USER_UID}.slice.d"
sudo tee /etc/systemd/system/user-\${USER_UID}.slice.d/limits.conf >/dev/null <<SYSTEMD_LIMITS
[Slice]
# Memory limit - prevent memory exhaustion
MemoryMax=90%
# CPU quota - dynamically calculated: 90% of \${TOTAL_CPUS} CPUs = \${CPU_QUOTA_PERCENT}%
CPUQuota=\${CPU_QUOTA_PERCENT}%
# CPU weight for fair scheduling (default is 100, range 1-10000)
# Lower weight = less priority when competing with other users
CPUWeight=100
SYSTEMD_LIMITS

# Reload systemd to apply slice changes
sudo systemctl daemon-reload
echo "Systemd slice limits configured for user $USERNAME (UID: \$USER_UID)"
echo "{message: \"  - CPUs available: \$TOTAL_CPUS.\", level: \"INFO\"}"
echo "{message: \"  - CPU quota set to: \${CPU_QUOTA_PERCENT}% (90% of total).\", level: \"INFO\"}"
EOF




# Add sudo access (if needed) to the script
if [ -n "${SUDO_COMMANDS:-}" ]; then
  cat >>script.sh <<EOF
  echo "$USERNAME ALL=(ALL) NOPASSWD: $SUDO_COMMANDS" | sudo tee -a /etc/sudoers.d/"$USERNAME"
  echo "{message: \"Successfully added sudo access.\", level: \"INFO\"}"
EOF
fi

# Copy opentelemetry config
if [[ -f /otelcol/config.yaml ]]; then
  ssh "${SSH_OPTS[@]}" "$SSH_HOST" \
    'sudo mkdir -p /etc/otelcol-contrib && sudo tee /etc/otelcol-contrib/config_mpc.yaml > /dev/null' \
    < /otelcol/config.yaml
else
  echo "Opentelemetry config not found, default one will be used"
fi

# Execute provision script on VM
SSH_PROVISION_OUTPUT=$(
    ssh "${SSH_OPTS[@]}" "$SSH_HOST" "bash -s" <script.sh 2>&1
) || {
    # If the command fails, the '||' block executes.
    # Note: Using '||' suppresses set -e for this line.
    echo "{message: \"Remote Provision Output: ${SSH_PROVISION_OUTPUT//$'\n'/ }\", level: \"WARNING\"}" >&2
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
    echo "{message: \"Remote Key Removal Output: ${SSH_KEY_RM_OUTPUT//$'\n'/ }\", level: \"WARNING\"}" >&2
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
