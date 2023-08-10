#!/bin/bash

DIR=`dirname $0`

docker build $DIR -t quay.io/sdouglas/registry:multiarch

docker push quay.io/sdouglas/registry:multiarch
