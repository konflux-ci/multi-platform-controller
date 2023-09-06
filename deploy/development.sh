#!/bin/sh

DIR=`dirname $0`

if [ -z "$QUAY_USERNAME" ]; then
    echo "Set QUAY_USERNAME"
    exit 1
fi


oc new-project multi-arch-controller
kubectl config set-context --current --namespace=multi-arch-controller

kubectl delete --ignore-not-found secret awskeys awsiam
oc create secret generic awskeys --from-file=id_rsa=/home/stuart/.ssh/sdouglas-arm-test.pem
oc create secret generic awsiam --from-literal=access-key-id=$MULTI_ARCH_ACCESS_KEY --from-literal=secret-access-key=$MULTI_ARCH_SECRET_KEY
kubectl label secrets awsiam build.appstudio.redhat.com/multi-arch-secret=true

echo "Installing the Operator"
rm -r $DIR/overlays/development
find $DIR -name dev-template -exec cp -r {} {}/../development \;
find $DIR -path \*development\*.yaml -exec sed -i s/QUAY_USERNAME/${QUAY_USERNAME}/ {} \;
kubectl apply -k $DIR/overlays/development

kubectl rollout restart deployment -n multi-arch-controller multi-arch-controller
oc project test-jvm-namespace
