#!/bin/bash

if [ "$#" -ne 2 ]; then
    echo "Illegal number of arguments."
    echo "Correct usage: hack/build-mariner-based-imgs/build-images.sh <vmm> <azure_devops_pat>"
    echo "Exiting..."
    exit 1
fi

vmm=$1
azureDevopsPat=$2

if [ $vmm == "ch" ]; then
    :
elif [ $vmm == "qemu" ]; then
    :
else
    echo "Invalid args: VMM should be one of \"ch\" or \"qemu\". Exiting..."
    exit 1
fi

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &>/dev/null && pwd)

DOCKER_BUILDKIT=1 docker build -t afo-builder -f $SCRIPT_DIR/Dockerfile-builder $SCRIPT_DIR

KUBEVIRT_COMPONENTS_TO_BUILD="${KUBEVIRT_COMPONENTS:-virt-operator virt-api virt-handler virt-controller virt-launcher}"
echo "Building KubeVirt Components: $KUBEVIRT_COMPONENTS_TO_BUILD"

# Building generic containers that are not dependent on the VMM
for ctr in virt-operator virt-api virt-handler virt-controller; do
    if [[ $KUBEVIRT_COMPONENTS_TO_BUILD == *"$ctr"* ]]; then
      echo "Building $ctr"
      DOCKER_BUILDKIT=1 docker build --build-arg BUILDER_IMAGE=afo-builder:latest \
          -t ${DOCKER_PREFIX}/${ctr}:${DOCKER_TAG} -f $SCRIPT_DIR/Dockerfile-${ctr} .
    fi
done

# Building the virt-launcher container separately because
# It might need a different base image when building for CloudHypervisor
ctr=virt-launcher
if [[ $KUBEVIRT_COMPONENTS_TO_BUILD == *"$ctr"* ]]; then
    echo "Building $ctr"
    DOCKER_BUILDKIT=1 docker --debug build \
        --build-arg AZURE_DEVOPS_PAT=${azureDevopsPat} \
        --build-arg BUILDER_IMAGE=afo-builder:latest \
        -t ${DOCKER_PREFIX}/${ctr}:${DOCKER_TAG} -f $SCRIPT_DIR/Dockerfile-${ctr}-${vmm} .
fi
