# Testing the OpenVMM Backend End-to-End

## 1. Getting the OpenVMM Binary

Download the OpenVMM binary using the instructions here: https://openvmm.dev/guide/user_guide/openvmm/run.html#pre-built-binaries

After downloading the binary, it should be placed in the following directory in this repo.

`./hack/build-mariner-based-imgs/openvmm`

## 2. Building the Container Images for KubeVirt Components

To build the KubeVirt components' container images, use the following script.

```bash
export DOCKER_PREFIX=docker.io/<username>       # you can also use ACR
export DOCKER_TAG=<tag>

./hack/build-mariner-based-imgs/build-images.sh -H openvmm

# Push the images to the container registry
./hack/build-mariner-based-imgs/push-images.sh
```

## 3. Generating the KubeVirt Manifest

Generate the KubeVirt manifest files (`kubevirt-operator.yaml` and `kubevirt-cr.yaml`) using the following commands.

```bash
export DOCKER_PREFIX=docker.io/harshitg       # you can also use ACR
export DOCKER_TAG=openvmm

./hack/build-mariner-based-imgs/make-mariner-manifests.sh
```

The manifest files will be produced at the `_out/manifests/release/` directory.

## 4. Setting Up the Kubernetes Cluster Using the Kind Provider

```bash
export KUBEVIRT_PROVIDER=kind-1.35
make cluster-up

export KUBECONFIG=$(realpath ./kubevirtci/_ci-configs/kind-1.35/.kubeconfig)
kubectl get pods -A
```

## 5. Installing KubeVirt

```bash
kubectl apply -f _out/manifests/release/kubevirt-operator.yaml

# Wait for the virt-operator pods to be running

kubectl apply -f _out/manifests/release/kubevirt-cr.yaml

# After all the pods (e.g., virt-handler) are in running state, apply the following label to the nodes.
# This custom label is needed because the PoC disables node labelling, but this one is needed for proper scheduling of VMI.
kubectl get nodes -o name | while read -r node; do
        kubectl label --overwrite "$node" "machine-type.node.kubevirt.io/q35=true"
done
```

## 6. Building the Disk Images for the KubeVirt Guest VM

### 6.1. Building an OS Disk Image Based on KubeVirt Upstream's Fedora Image

Convert the QCOW2 image format to RAW.

```bash
# Create a container from the image containing the QCOW2 file
ctr=$(docker create --name src quay.io/kubevirt/fedora-with-test-tooling-container-disk:devel /bin/bash)
# Copy the QCOW2 file from the container to the local filesystem
docker cp $ctr:/disk/provisioned-image.qcow2 .

qemu-img convert -f qcow2 -O raw provisioned-image.qcow2 disk.raw

# Build the container image containing RAW OS image file

cat > Dockerfile <<'EOF'
FROM scratch
ADD --chown=107:107 disk.raw /disk/
EOF

docker build -t docker.io/harshitg/fedora-with-test-tooling-container-disk:raw -f .
docker push docker.io/harshitg/fedora-with-test-tooling-container-disk:raw
```

### 6.2. Building an Image Containing the Linux Kernel for Direct Boot

Obtain an AzureLinux kernel image `vmlinux.bin` and follow the following steps.

```bash
cat > Dockerfile <<'EOF'
FROM scratch
ADD --chown=107:107 vmlinux.bin /boot/
EOF

docker build -t docker.io/harshitg/kernel:test -f .
docker push docker.io/harshitg/kernel:test
```

## 7. Create a Virtual Machine Instance (VMI)

Save the following manifest into `vmi.yaml`.

```yaml
apiVersion: kubevirt.io/v1
kind: VirtualMachineInstance
metadata:
  labels:
    special: vmi-fedora
  name: vmi-fedora
spec:
  domain:
    firmware:
      kernelBoot:
        container:
          image: docker.io/harshitg/kernel:test
          kernelPath: /boot/vmlinux.bin
        kernelArgs: "root=/dev/vda1 console=ttyS0 cgroup_no_v1=all systemd.unified_cgroup_hierarchy=1"
    devices:
      disks:
      - disk:
          bus: virtio
        name: containerdisk
      interfaces:
      - masquerade: {}
        name: default
      rng: {}
    memory:
      guest: 1024M
    resources: {}
  networks:
  - name: default
    pod: {}
  terminationGracePeriodSeconds: 0
  volumes:
  - containerDisk:
      image: docker.io/harshitg/fedora-with-test-tooling-container-disk:raw
    name: containerdisk
```

```bash
kubectl apply -f vmi.yaml
```

## 8. Accessing the VMI Console

```bash
virtctl console vmi-fedora

# Login credentials are fedora/fedora
```

## 9. Inspecting the OpenVMM Process

```bash
# Exec into the virt-launcher pod created for the above VMI
kubectl exec -it pod/virt-launcher-xxxx -- /bin/bash

# In the virt-launcher pod, you can run ps -aux to find the OpenVMM process
```
