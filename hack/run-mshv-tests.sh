#!/bin/bash

export KUBEVIRT_PROVIDER=external
export PULL_POLICY=Always
export KUBECONFIG=/path/to/kubeconfig
export DOCKER_PREFIX=[your registry]

make cluster-up

make cluster-sync

export KUBEVIRT_FUNC_TEST_SUITE_ARGS="--ginkgo.dry-run --ginkgo.v --ginkgo.focus-file=kubectl_test.go"
#export KUBEVIRT_E2E_FOCUS="test_id:3812"
#export KUBEVIRT_FUNC_TEST_SUITE_ARGS="--ginkgo.dry-run --ginkgo.v"

export DOCKER_PREFIX=harshitg

make functest

#./_out/tests/tests.test --ginkgo.v --ginkgo.focus=test_id:3812 -kubeconfig=/tmp/kubeconfig_dir/kubeconfig -config=./tests/default-config.json -kubectl-path=/usr/local/bin/kubectl
