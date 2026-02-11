# Acceptance tests

## Required Tools

* chainsaw
* docker
* kind
* kustomize
* kubectl


## Setup

Use the `prepare_cluster.sh` script to setup a Kind environment.

```bash
./hack/prepare_cluster.sh
```

## Run the tests

Use the Chainsaw CLI to run the tests

```bash
chainsaw test .
```
