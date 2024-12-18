#!/bin/sh

DIR=`dirname $0`
TEMP_DIR="${DIR}/../.tmp"

if [ -z "$QUAY_USERNAME" ]; then
    echo "Set QUAY_USERNAME"
    exit 1
fi


kubectl create ns multi-platform-controller --dry-run=client -o yaml | kubectl apply -f -
kubectl config set-context --current --namespace=multi-platform-controller

kubectl delete --ignore-not-found secret awskeys awsiam ibmiam
kubectl create secret generic awskeys --from-file=id_rsa=$ID_RSA_KEY
kubectl create secret generic awsiam --from-literal=access-key-id=$MULTI_ARCH_ACCESS_KEY --from-literal=secret-access-key=$MULTI_ARCH_SECRET_KEY
kubectl create secret generic ibmiam --from-literal=api-key=$IBM_CLOUD_API_KEY
kubectl label secrets awsiam build.appstudio.redhat.com/multi-platform-secret=true
kubectl label secrets ibmiam build.appstudio.redhat.com/multi-platform-secret=true
kubectl label secrets awskeys build.appstudio.redhat.com/multi-platform-secret=true

# Check Tekton
kubectl describe crd pipelinerun >/dev/null 2>&1
if [ $? -eq 0 ]; then
  echo "Tekton API already installed"
else
  echo "Tekton API not found, installing"
  kubectl apply --filename https://storage.googleapis.com/tekton-releases/pipeline/latest/release.yaml
  # If you need full Tekton operator, comment the line above and uncomment below:
  # kubectl apply --filename https://storage.googleapis.com/tekton-releases/operator/latest/release.yaml
fi

echo "Installing the Operator"
rm -r $DIR/overlays/development
find $DIR -name dev-template -exec cp -r {} {}/../development \;
find $DIR -path \*development\*.yaml -exec sed -i s/QUAY_USERNAME/${QUAY_USERNAME}/ {} \;
kubectl apply -k $DIR/overlays/development

kubectl rollout restart deployment -n multi-platform-controller multi-platform-controller

echo "Creating OTP TLS secret"
kubectl create secret tls otp-tls-secrets --key ${TEMP_DIR}/k8s/certs/ca.key --cert ${TEMP_DIR}/k8s/certs/ca.crt --dry-run=client -o yaml | kubectl apply -f -
kubectl rollout restart deployment -n multi-platform-controller multi-platform-otp-server
