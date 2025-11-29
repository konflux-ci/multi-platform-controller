#!/bin/sh
# Cleanup script for EC2 key pair created during e2e tests
# This script deletes the EC2 key pair created for the GitHub Actions workflow run.
# Note: This script does not use 'set -e' to allow cleanup to continue even if the key pair doesn't exist

SUFFIX="${1:?SUFFIX must be provided as argument}"
AWS_SSH_KEY_NAME="multi-platform-controller-${SUFFIX}"

echo "Deleting AWS EC2 key pair: $AWS_SSH_KEY_NAME"
aws ec2 delete-key-pair --key-name "$AWS_SSH_KEY_NAME" 2>/dev/null || echo "Key pair $AWS_SSH_KEY_NAME not found or already deleted"
