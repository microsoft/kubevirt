#!/bin/bash

set -e

print_usage() {
    echo "This is a script to push the built KubeVirt images."
    echo "Before running this script, export the following env variables:"
    echo "    - DOCKER_PREFIX: prefix to the URL of the container images."
    echo "                      e.g., export DOCKER_PREFIX=acrafoimages.azurecr.io/kubevirt-mshv/"
    echo "    - DOCKER_TAG: image tag for the container images."
    echo "                      e.g., export DOCKER_TAG=20241112-1"
    echo "    - KUBEVIRT_COMPONENTS: which components of KubeVirt to push. If empty, push all components"
    echo "                      e.g.,KUBEVIRT_COMPONENTS='virt-launcher virt-controller'"
    echo ""
    echo "Script usage: ./push-images.sh"
}

if [ -z "$DOCKER_PREFIX" ] || [ -z "$DOCKER_TAG" ]; then
    echo "Error: This script requires DOCKER_PREFIX and DOCKER_TAG env vars to be set before calling it."
    print_usage
    exit 1
fi

while getopts ":h" option; do
   case $option in
      h) # display Help
          print_usage
          exit;;
     \?) # Invalid option
          echo "Error: Invalid option"
          exit;;
   esac
done


SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &>/dev/null && pwd)

KUBEVIRT_COMPONENTS_TO_PUSH="${KUBEVIRT_COMPONENTS:-virt-operator virt-api virt-handler virt-controller virt-launcher}"
echo "Pushing KubeVirt Components: $KUBEVIRT_COMPONENTS_TO_PUSH"

for ctr in virt-launcher virt-operator virt-api virt-handler virt-controller; do
    if [[ $KUBEVIRT_COMPONENTS_TO_PUSH == *"$ctr"* ]]; then
        echo "Pushing $ctr"
        docker push ${DOCKER_PREFIX}/${ctr}:${DOCKER_TAG}
    fi
done
