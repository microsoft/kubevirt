#!/bin/bash

print_usage() {
    echo "This is a script to build the KubeVirt images."
    echo "Before running this script, export the following env variables:"
    echo "    - DOCKER_PREFIX: prefix to the URL of the container images to be built."
    echo "                      e.g., export DOCKER_PREFIX=acrafoimages.azurecr.io/kubevirt-mshv/"
    echo "    - DOCKER_TAG: image tag for the container images to be built."
    echo "                      e.g., export DOCKER_TAG=20241112-1"
    echo ""
    echo "Script usage: ./build-images.sh"
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
        exit
        ;;
    \?) # Invalid option
        echo "Error: Invalid option"
        exit
        ;;
    esac
done

set -e

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &>/dev/null && pwd)

DOCKER_BUILDKIT=1 docker build -t afo-builder -f $SCRIPT_DIR/Dockerfile-builder $SCRIPT_DIR

DOCKER_BUILDKIT=1 docker --debug build \
    --build-arg BUILDER_IMAGE=afo-builder:latest \
    -t ${DOCKER_PREFIX}/virt-launcher:${DOCKER_TAG} -f $SCRIPT_DIR/Dockerfile-virt-launcher .

docker push ${DOCKER_PREFIX}/virt-launcher:${DOCKER_TAG}

docker system prune -f
