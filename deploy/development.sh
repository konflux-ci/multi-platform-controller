#!/bin/sh

DIR=`dirname $0`

if [ -z "$QUAY_USERNAME" ]; then
    echo "Set QUAY_USERNAME"
    exit 1
fi


oc new-project multi-platform-controller
kubectl config set-context --current --namespace=multi-platform-controller

kubectl delete --ignore-not-found secret awskeys awsiam ibmiam
oc create secret generic awskeys --from-file=id_rsa=/home/stuart/.ssh/sdouglas-arm-test.pem
oc create secret generic awsiam --from-literal=access-key-id=$MULTI_ARCH_ACCESS_KEY --from-literal=secret-access-key=$MULTI_ARCH_SECRET_KEY
oc create secret generic ibmiam --from-literal=api-key=$IBM_CLOUD_API_KEY
kubectl label secrets awsiam build.appstudio.redhat.com/multi-platform-secret=true
kubectl label secrets ibmiam build.appstudio.redhat.com/multi-platform-secret=true
kubectl label secrets awskeys build.appstudio.redhat.com/multi-platform-secret=true

echo "Installing the Operator"
rm -r $DIR/overlays/development
find $DIR -name dev-template -exec cp -r {} {}/../development \;
find $DIR -path \*development\*.yaml -exec sed -i s/QUAY_USERNAME/${QUAY_USERNAME}/ {} \;
kubectl apply -k $DIR/overlays/development

kubectl rollout restart deployment -n multi-platform-controller multi-platform-controller
oc project test-jvm-namespace
