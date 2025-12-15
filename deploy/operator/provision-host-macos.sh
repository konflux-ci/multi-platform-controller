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

cd /tmp
mkdir -p /root/.ssh
cp "$SSH_WORKSPACE_PATH/id_rsa" /tmp/master_key
chmod 0400 /tmp/master_key
export SSH_HOST="$USER@$HOST"
SSH_OPTS=(-i /tmp/master_key -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null)

# MacOS Instance sometimes takes a little longer to be ready, so we need to wait for it
sleep 30

export USERNAME=konflux-builder
# Copy/configure remote SSH key and then delete on VM
ssh "${SSH_OPTS[@]}" "${SSH_HOST}" "cat /Users/${USER}/${USERNAME}" > id_rsa
echo "{message: \"Successfully copied remote SSH key from VM.\", level: \"INFO\"}"
SSH_KEY_RM_OUTPUT=$(
    ssh "${SSH_OPTS[@]}" "${SSH_HOST}" "sudo rm /Users/${USER}/${USERNAME}"
) || {
    # If the command fails, the '||' block executes.
    # Note: Using '||' suppresses set -e for this line.
    echo "{message: \"Remote Key Removal Output: $SSH_KEY_RM_OUTPUT\", level: \"WARNING\"}" >&2
    exit 1
}
echo "{message: \"Successfully deleted SSH key from VM.\", level: \"INFO\"}"

chmod 0400 id_rsa
ENCODED_HOST=$(echo "${USERNAME}@${HOST}" | base64 -w 0)
DIR=$(echo "/Users/${USERNAME}" | base64 -w 0)

# Generate a Kubernetes Secret file based on the existence of a certificate
if [ -e "/tls/tls.crt" ]; then
    echo "{message: \"Creating secret file using TLS certificate...\", level: \"INFO\"}"
    KEY=$(cat id_rsa)
    OTP=$(curl --cacert /tls/tls.crt -XPOST -d "$KEY" https://multi-platform-otp-server.multi-platform-controller.svc.cluster.local/store-key | base64 -w 0)
    OTP_SERVER="$(echo https://multi-platform-otp-server.multi-platform-controller.svc.cluster.local/otp | base64 -w 0)"
    echo $OTP | base64 -d

    cat >secret.yaml <<EOF
    apiVersion: v1
    data:
        otp-ca: "$(cat /tls/tls.crt | base64 -w 0)"
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
    KEY=$(cat id_rsa | base64 -w 0)

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
