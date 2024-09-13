#!/bin/bash

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &>/dev/null && pwd)

KUBEVIRT_COMPONENTS_TO_PUSH="${KUBEVIRT_COMPONENTS:-virt-operator virt-api virt-handler virt-controller virt-launcher}"
echo "Pushing KubeVirt Components: $KUBEVIRT_COMPONENTS_TO_PUSH"

for ctr in virt-launcher virt-operator virt-api virt-handler virt-controller; do
    if [[ $KUBEVIRT_COMPONENTS_TO_PUSH == *"$ctr"* ]]; then
        echo "Pushing $ctr"
        docker push ${DOCKER_PREFIX}/${ctr}:${DOCKER_TAG}
    fi
done
