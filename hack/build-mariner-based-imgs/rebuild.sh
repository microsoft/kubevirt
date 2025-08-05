#!/bin/bash
set -e

SCRIPT_DIRECTORY="$(dirname $(realpath "$0"))"

az acr login -n acrafoimages.azurecr.io

$SCRIPT_DIRECTORY/build-images.sh -H openvmm
$SCRIPT_DIRECTORY/make-mariner-manifests.sh
$SCRIPT_DIRECTORY/push-images.sh

# On the build machine
TestMachineIp=10.137.196.60

scp /home/jocelynb/src/www.github.com/microsoft/kubevirt/_out/manifests/release/kubevirt-operator.yaml root@$TestMachineIp:/root/
scp /home/jocelynb/src/www.github.com/microsoft/kubevirt/_out/manifests/release/kubevirt-cr.yaml root@$TestMachineIp:/root/
