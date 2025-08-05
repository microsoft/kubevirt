#!/bin/bash

print_usage() {
    echo "This is a script to build KubeVirt manifests."
    echo "Before running this script, export the following env variables:"
    echo "    - DOCKER_PREFIX: prefix to the URL of the KubeVirt container images to refer to in the manifest."
    echo "                      e.g., export DOCKER_PREFIX=acrafoimages.azurecr.io/kubevirt-mshv/"
    echo "    - DOCKER_TAG: image tag for the KubeVirt container images to refer to in the manifest."
    echo "                      e.g., export DOCKER_TAG=20241112-1"
    echo ""
    echo "Script usage: ./make-mariner-manifests.sh"
}

if [ -z "$DOCKER_PREFIX" ] || [ -z "$DOCKER_TAG" ]; then
    echo "Error: This script requires DOCKER_PREFIX and DOCKER_TAG env vars to be set before calling it."
    print_usage
    exit 1
fi

while getopts ":hH:P:" option; do
    case $option in
    h) # display Help
        print_usage
        exit
        ;;
    esac
done

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &>/dev/null && pwd)

cd $SCRIPT_DIR/../../

export KUBEVIRT_ONLY_USE_TAGS=true
export FEATURE_GATES="Root,CPUManager,DataVolumes,HostDevices,NUMA"

make manifests

cd -
