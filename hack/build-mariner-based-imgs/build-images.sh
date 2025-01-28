#!/bin/bash

print_usage() {
    echo "This is a script to build the KubeVirt images."
    echo "Before running this script, export the following env variables:"
    echo "    - DOCKER_PREFIX: prefix to the URL of the container images to be built."
    echo "                      e.g., export DOCKER_PREFIX=acrafoimages.azurecr.io/kubevirt-mshv/"
    echo "    - DOCKER_TAG: image tag for the container images to be built."
    echo "                      e.g., export DOCKER_TAG=20241112-1"
    echo "    - KUBEVIRT_COMPONENTS: which components of KubeVirt to build. If empty, build all components"
    echo "                      e.g.,KUBEVIRT_COMPONENTS='virt-launcher virt-controller'"
    echo ""
    echo "Script usage: ./build-images.sh -H <hypervisor (ch/qemu)> -P <azure-devops-pat> [-R <additional-rpms-dir>]"
    echo "                  hypervisor needs to be one of 'ch' and 'qemu'"
}

if [ -z "$DOCKER_PREFIX" ] || [ -z "$DOCKER_TAG" ]; then
    echo "Error: This script requires DOCKER_PREFIX and DOCKER_TAG env vars to be set before calling it."
    print_usage
    exit 1
fi

while getopts ":hH:P:R:" option; do
   case $option in
      h) # display Help
          print_usage
          exit;;
      H) # Enter the hypervisor
          hypervisor=$OPTARG
          echo "hypervisor = $hypervisor"
          case $hypervisor in
            ch)
              ;&
            qemu)
              vmm=$hypervisor;;
            *)
              echo "Error: Invalid hypervisor argument $hypervisor."
              echo ""
              print_usage
              echo "Exiting..."
              exit 1
          esac
          ;;
      R) # Directory containing additional RPMs
          additionalRpmsDir=$OPTARG;;
      P) # Personal Access Token to access repos in LSG Azure DevOps Org
          azureDevopsPat=$OPTARG;;
     \?) # Invalid option
          echo "Error: Invalid option"
          exit;;
   esac
done

if [ -z "$vmm" ]; then
    echo "Error: Required argument -H <hypervisor> not specified"
    print_usage
    exit 1
fi
if [ -z "$azureDevopsPat" ]; then
    echo "Error: Required argument -P <azure-devops-pat> not specified"
    print_usage
    exit 1
fi

set -e

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &>/dev/null && pwd)

if [ -z "$additionalRpmsDir" ]; then
  additionalRpmsDir=./additionalRpms/
  mkdir -p $additionalRpmsDir 
fi

echo "Creating an RPM repository in additionalRpmsDir: ${additionalRpmsDir}"
pushd $additionalRpmsDir
createrepo .
popd

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

    docker system prune -f
done

# Building the virt-launcher container separately because
# It might need a different base image when building for CloudHypervisor
ctr=virt-launcher
if [[ $KUBEVIRT_COMPONENTS_TO_BUILD == *"$ctr"* ]]; then
    echo "Building $ctr"
    DOCKER_BUILDKIT=1 docker --debug build \
        --build-arg AZURE_DEVOPS_PAT=${azureDevopsPat} \
        --build-arg BUILDER_IMAGE=afo-builder:latest \
        --build-arg ADDITIONAL_RPMS_DIR=${additionalRpmsDir} \
        -t ${DOCKER_PREFIX}/${ctr}:${DOCKER_TAG} -f $SCRIPT_DIR/Dockerfile-${ctr}-${vmm} .

    docker system prune -f
fi
