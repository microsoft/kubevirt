# Building AzLinux 2.0 Images for Cloud-HYpervisor/MSHV

This guide provides instructions on how to compile the source code in this repository and build container images that can run KubeVirt on a cloud-hypervisor and MSHV based virtualization stack.

## Prerequisites

1. Create a Personal Access Token on Azure DevOps that can **read** the repository [cloud-hypervisor](https://microsoft.visualstudio.com/LSG/_git/cloud-hypervisor) in the [LSG AzureDevOps Project](https://microsoft.visualstudio.com/LSG).

2. Create an account on Docker Hub. Decide the name of the tag to use for the Kubevirt containers, e.g., `test1`. If you want to use another Docker registry, please update the `DOCKER_PREFIX` environment variable accordingly. This document assumes that containers will be stored in Docker Hub.

## Build Container Images

1. **Set the environment variables.**

IMPORTANT: All the steps below rely on these environment variables.

```bash
export DOCKER_PREFIX=docker.io/<dockerhub_username>
export DOCKER_TAG=<container_img_tag>
```

2. **Build the container images.** <br/>
The `build-images` script needs two arguments - the hypervisor for which the image is being built (either `qemu` or `ch`) and the Azure DevOps Personal Access Token for cloning the [cloud-hypervisor](https://microsoft.visualstudio.com/LSG/_git/cloud-hypervisor) repository.

```bash
./hack/build-mariner-based-imgs/build-images.sh <hypervisor> <azure_devops_pat>
```

In case you want to build only a specific KubeVirt component, e.g., `virt-launcher`, you can use the build argument `KUBEVIRT_COMPONENTS` to specify that. For example, the code below only builds the `virt-handler` and `virt-launcher` containers.

```bash
KUBEVIRT_COMPONENTS="virt-handler virt-launcher" \
    hack/build-mariner-based-imgs/build-images.sh <hypervisor> <azure_devops_pat>
```

3. **Push the container images**<br/>

Push the built containers to the container registry.

```bash
hack/build-mariner-based-imgs/push-images.sh
```

In case you want to push a specific KubeVirt component, e.g., `virt-launcher`, you can use the argument `KUBEVIRT_COMPONENTS` to specify that. For example, the code below only pushes the `virt-handler` and `virt-launcher` containers.

```bash
KUBEVIRT_COMPONENTS="virt-handler virt-launcher" \
    hack/build-mariner-based-imgs/push-images.sh
```

4. **Prepare Kubernetes Manifest files.**<br/>

Create manifest files for installing KubeVirt on a Kubernetes cluster.

```bash
hack/build-mariner-based-imgs/make-mariner-manifests.sh
```

The generated manifests will be stored in the `_out/manifests/release/` directory.