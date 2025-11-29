#!/bin/sh
# Cleanup script for EC2 instances created during e2e tests
# This script finds and terminates EC2 instances tagged with the instance tag
# from the GitHub Actions workflow run.
# Note: This script does not use 'set -e' to allow cleanup to continue even if some operations fail
#
# Usage: cleanup-ec2-instances.sh INSTANCE_TAG
#   INSTANCE_TAG: The instance tag to use for filtering instances

INSTANCE_TAG="${1:?INSTANCE_TAG must be provided as argument}"
echo "Finding EC2 instances with tag multi-platform-instance=${INSTANCE_TAG}"

# Get all instance IDs with the matching tags
INSTANCE_IDS=$(aws ec2 describe-instances \
  --filters \
    "Name=tag:multi-platform-instance,Values=${INSTANCE_TAG}" \
    "Name=tag:MultiPlatformManaged,Values=true" \
    "Name=instance-state-name,Values=running,pending,stopped,stopping" \
  --query 'Reservations[*].Instances[*].InstanceId' \
  --output text 2>/dev/null || echo "")

if [ -z "$INSTANCE_IDS" ] || [ "$INSTANCE_IDS" = "None" ]; then
  echo "No EC2 instances found with tag multi-platform-instance=${INSTANCE_TAG}"
else
  echo "Terminating EC2 instances: $INSTANCE_IDS"
  # Convert space-separated IDs to newline-separated for better output
  for INSTANCE_ID in $INSTANCE_IDS; do
    echo "Terminating instance: $INSTANCE_ID"
    aws ec2 terminate-instances --instance-ids "$INSTANCE_ID" || echo "Failed to terminate instance $INSTANCE_ID"
  done
fi

