#!/bin/sh

DIR=`dirname $0`

if [ -z "$QUAY_USERNAME" ]; then
    echo "Set QUAY_USERNAME"
    exit 1
fi


oc new-project multi-arch-operator
kubectl config set-context --current --namespace=multi-arch-operator

oc create secret generic aws-keys --from-file=id_rsa=/home/stuart/.ssh/sdouglas-arm-test.pem

kubectl label secrets hard-coded-host appstudio.io/build-host-pool=arm64

echo "Installing the Operator"
rm -r $DIR/overlays/development
find $DIR -name dev-template -exec cp -r {} {}/../development \;
find $DIR -path \*development\*.yaml -exec sed -i s/QUAY_USERNAME/${QUAY_USERNAME}/ {} \;
kubectl apply -k $DIR/overlays/development

kubectl rollout restart deployment -n multi-arch-operator multi-arch-operator
