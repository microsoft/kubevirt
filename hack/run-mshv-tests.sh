#!/bin/bash

export KUBEVIRT_PROVIDER=external
export PULL_POLICY=Always
export KUBECONFIG=/path/to/kubeconfig
export DOCKER_PREFIX=[your registry]


make cluster-up

make cluster-syn
