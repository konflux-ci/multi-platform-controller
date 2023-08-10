#!/bin/sh

DIR=`dirname $0`

if [ -z "$QUAY_USERNAME" ]; then
    echo "Set QUAY_USERNAME"
    exit 1
fi


oc new-project multi-arch-controller
kubectl config set-context --current --namespace=multi-arch-controller

oc create secret generic awskeys --from-file=id_rsa=/home/stuart/.ssh/sdouglas-arm-test.pem

echo "Installing the Operator"
rm -r $DIR/overlays/development
find $DIR -name dev-template -exec cp -r {} {}/../development \;
find $DIR -path \*development\*.yaml -exec sed -i s/QUAY_USERNAME/${QUAY_USERNAME}/ {} \;
kubectl apply -k $DIR/overlays/development

kubectl rollout restart deployment -n multi-arch-controller multi-arch-controller
