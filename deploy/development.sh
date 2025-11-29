#!/bin/sh
set -e

DIR=`dirname $0`

if [ -z "$QUAY_USERNAME" ]; then
    echo "Set QUAY_USERNAME"
    exit 1
fi


kubectl create namespace -o yaml --dry-run=client multi-platform-controller | kubectl apply -f -
kubectl config set-context --current --namespace=multi-platform-controller

# Optionally create SSH key if ID_RSA_KEY_PATH is not set
if [ -z "$ID_RSA_KEY_PATH" ]; then
    echo "ID_RSA_KEY_PATH not set, creating new SSH key pair using AWS EC2..."
    if [ -n "$GITHUB_RUN_ID" ]; then
        UNIQUE_ID="$GITHUB_RUN_ID"
    else
        UNIQUE_ID=$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n')
    fi
    AWS_SSH_KEY_NAME="multi-platform-controller-${UNIQUE_ID}"
    ID_RSA_KEY_PATH="/tmp/${AWS_SSH_KEY_NAME}.pem"

    # Get the current AWS identity ARN and convert assumed-role ARN to base role ARN for tagging
    # The policy expects the base role ARN (arn:aws:iam::...) not the assumed-role ARN (arn:aws:sts::...)
    ASSUMED_ROLE_ARN=$(aws sts get-caller-identity --query 'Arn' --output text 2>&1)
    if [ $? -eq 0 ] && [ -n "$ASSUMED_ROLE_ARN" ]; then
        # Convert assumed-role ARN to base role ARN
        # arn:aws:sts::ACCOUNT:assumed-role/ROLE-NAME/SESSION -> arn:aws:iam::ACCOUNT:role/ROLE-NAME
        CREATOR_ROLE_ARN=$(echo "$ASSUMED_ROLE_ARN" | sed 's|arn:aws:sts::|arn:aws:iam::|' | sed 's|assumed-role/\([^/]*\)/.*|role/\1|')
    else
        echo "Warning: Failed to get AWS caller identity: $ASSUMED_ROLE_ARN"
        CREATOR_ROLE_ARN=""
    fi

    # Create key pair with CreatorRole tag and write key material directly to file
    # Errors are printed to stderr
    if [ -n "$CREATOR_ROLE_ARN" ]; then
        echo "Creating key pair with CreatorRole tag: $CREATOR_ROLE_ARN"
        # Use a temporary file for tag-specifications to properly handle ARN with special characters
        TAG_SPEC_FILE=$(mktemp)
        cat > "$TAG_SPEC_FILE" <<EOF
[
  {
    "ResourceType": "key-pair",
    "Tags": [
      {
        "Key": "CreatorRole",
        "Value": "${CREATOR_ROLE_ARN}"
      }
    ]
  }
]
EOF
        # Redirect stdout (key material) directly to file, stderr goes to stderr (printed)
        aws ec2 create-key-pair --key-name "$AWS_SSH_KEY_NAME" \
            --tag-specifications "file://${TAG_SPEC_FILE}" \
            --query 'KeyMaterial' --output text > "$ID_RSA_KEY_PATH"
        CREATE_KEY_EXIT_CODE=$?
        rm -f "$TAG_SPEC_FILE"
    else
        # Fallback if we can't get the role ARN
        echo "Warning: No role ARN available, creating key pair without CreatorRole tag"
        # Redirect stdout (key material) directly to file, stderr goes to stderr (printed)
        aws ec2 create-key-pair --key-name "$AWS_SSH_KEY_NAME" \
            --query 'KeyMaterial' --output text > "$ID_RSA_KEY_PATH"
        CREATE_KEY_EXIT_CODE=$?
    fi

    if [ $CREATE_KEY_EXIT_CODE -eq 0 ] && [ -s "$ID_RSA_KEY_PATH" ]; then
        chmod 600 "$ID_RSA_KEY_PATH"
        echo "Created SSH key pair: $AWS_SSH_KEY_NAME"
    else
        echo "Error: Failed to create SSH key pair." >&2
        exit 1
    fi
fi

(
    # Explicitly disable tracing
    set +x

    ID_RSA_KEY_PATH="${ID_RSA_KEY_PATH:?"ID_RSA_KEY_PATH is not set"}"
    AWS_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID:?"AWS_ACCESS_KEY_ID is not set"}"
    AWS_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY:?"AWS_SECRET_ACCESS_KEY is not set"}"
    AWS_SESSION_TOKEN="${AWS_SESSION_TOKEN:?"AWS_SESSION_TOKEN is not set"}"
    # TODO: Enable when the authentication in the CI is configured for IBM Cloud
    # IBM_CLOUD_API_KEY="${IBM_CLOUD_API_KEY:?"IBM_CLOUD_API_KEY is not set"}"

    kubectl delete --ignore-not-found secret awskeys awsiam ibmiam

    kubectl create secret generic awskeys \
    --from-file=id_rsa="${ID_RSA_KEY_PATH}"

    kubectl create secret generic awsiam \
        --from-literal=access-key-id="${AWS_ACCESS_KEY_ID}" \
        --from-literal=secret-access-key="${AWS_SECRET_ACCESS_KEY}" \
        --from-literal=session-token="${AWS_SESSION_TOKEN}"

    kubectl create secret generic ibmiam \
    --from-literal=api-key="${IBM_CLOUD_API_KEY}"

    kubectl label secrets awsiam build.appstudio.redhat.com/multi-platform-secret=true
    kubectl label secrets ibmiam build.appstudio.redhat.com/multi-platform-secret=true
    kubectl label secrets awskeys build.appstudio.redhat.com/multi-platform-secret=true
)

echo "Installing the Operator"
rm -rf $DIR/overlays/development
find $DIR -name dev-template -exec cp -r {} {}/../development \;
find $DIR -path \*development\*.yaml -exec sed -i s/QUAY_USERNAME/${QUAY_USERNAME}/ {} \;
find $DIR/operator $DIR/otp -name "*.yaml" -exec sed -i 's/imagePullPolicy: Always/imagePullPolicy: IfNotPresent/' {} \;

# Generate a unique instance tag if not provided
# Accept instance tag from environment variable, or generate it
if [ -z "$INSTANCE_TAG" ]; then
    if [ -n "$GITHUB_RUN_ID" ]; then
        INSTANCE_TAG="${GITHUB_RUN_ID}-development"
    else
        INSTANCE_TAG="${QUAY_USERNAME}-development"
    fi
fi
find $DIR -path \*development\*.yaml -exec sed -i s/INSTANCE_TAG/${INSTANCE_TAG}/ {} \;

# Set the SSH key name
find $DIR -path \*development\*.yaml -exec sed -i s/AWS_SSH_KEY_NAME/${AWS_SSH_KEY_NAME:?"AWS_SSH_KEY_NAME is not set"}/ {} \;


# Generate self signed certificate for OTP
OTP_CERTS=$(mktemp -d)
GENCERTS_DIR="$OTP_CERTS" "$DIR/generate_ca.sh"
echo "Creating OTP TLS secret"
kubectl create secret tls otp-tls-secrets --key ${OTP_CERTS}/ca.key --cert ${OTP_CERTS}/ca.crt --dry-run=client -o yaml | kubectl apply -f -

kubectl apply -k $DIR/overlays/development
kubectl rollout restart deployment -n multi-platform-controller multi-platform-controller
