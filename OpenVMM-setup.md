# Testing the OpenVMM Backend End-to-End

## Overview

This proof of concept runs KubeVirt VirtualMachineInstances with OpenVMM instead of libvirt/QEMU. It implements the existing virt-launcher `DomainManager` interface and maps a limited VMI specification directly to an OpenVMM command line. The supported configuration includes a single (vmbus/virtio-blk) disk exposed via containerDisk or PVC, a  TAP-backed vmbus/virtio-net interface, UEFI and direct Linux kernel boot, a serial console accessible through `virtctl` and VNC access to VM. Unmodified Azure marketplace VHD can be used to create VM with the vmbus transport.

The main code changes:

- Add an OpenVMM-backed `DomainManager` and configure virt-launcher to launch and monitor the OpenVMM process directly.
- Add a new type of disk bus (`vmbus`) and a new network interface model (`vmbus`) that instructs OpenVMM to use the VMBus transport for that device.
- Resolve ImageVolume container disks and expose them through the paths expected by virt-launcher.
- Allow user to create a VMI based on a PVC, as long as it contains a `disk.img` file in the volume.
- Provide OpenVMM with a controlling PTY while relaying the guest serial console through a Unix socket.
- Configure OpenVMM to listen for VNC connections on `0.0.0.0:5900`. Implement a VNC traffic forwarder to move bytes to/from the TCP port and the UNIX socket created by KubeVirt for VNC access via `virtctl vnc` (`virt-vnc`).
- Package the OpenVMM binary and MSVM firmware in the `virt-launcher` image.
- Generate node capability XML through QEMU emulation, because LibVirt and QEMU packages in AzureLinux are not are not tested against MSHV.

This is a focused PoC rather than a complete replacement for the libvirt backend. Features outside the supported VMI subset, including migration and most advanced device and lifecycle operations, are not implemented.

## Prerequisites

Run the PoC on a Kubernetes cluster whose nodes expose `/dev/mshv` as the hypervisor device. The build and manifest-generation scripts used below configure KubeVirt to use the MSHV (`hyperv-direct`) hypervisor backend. OpenVMM will not start on nodes where `/dev/mshv` is unavailable or inaccessible to the virt-launcher pod.

If you need to run the PoC on a KVM node, then update the `kubevirt-cr.yaml` manifest and set `hypervisor: kvm`. Note that this has not been tested.

## 1. Getting the OpenVMM Binary and MSVM firmware

Download the OpenVMM binary using the instructions here: https://openvmm.dev/guide/user_guide/openvmm/run.html#pre-built-binaries

Next, get the `MSVM.fd` firmware by following OpenVMM guide: https://openvmm.dev/guide/reference/devices/firmware/mu_msvm_uefi.html

After downloading the binaries, they should be placed in the following directory in this repo.

`./hack/build-openvmm-virt-launcher/openvmm/`


## 2. Building the Container Images for KubeVirt Components

### 2.1 First build the vanilla KubeVirt Components

Since we have only modified the `virt-launcher` component, the rest of the KubeVirt components' container images can be built using the existing build scripts.

```bash
export DOCKER_PREFIX=docker.io/<username>       # you can also use ACR
export DOCKER_TAG=<tag>

make bazel-build-images && make bazel-push-images
```

### 2.2 Then build OpenVMM Virt-Launcher separately

To build and push the KubeVirt component `virt-launcher` container image, use the following script. Since we're using the same `DOCKER_TAG`, it will overwrite the vanilla `virt-launcher` image built in the previous step.

```bash
export DOCKER_PREFIX=docker.io/<username>       # you can also use ACR
export DOCKER_TAG=<tag>

./hack/build-openvmm-virt-launcher/build-images.sh
```

## 3. Generating the KubeVirt Manifest

Generate the KubeVirt manifest files (`kubevirt-operator.yaml` and `kubevirt-cr.yaml`) using the following commands.

```bash
export DOCKER_PREFIX=docker.io/<username>       # you can also use ACR
export DOCKER_TAG=<tag>

./hack/build-openvmm-virt-launcher/make-manifests.sh
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

# After all the pods (e.g., virt-handler) are in running state, we can say that the KubeVirt deployment is Ready.
```

## 6. Creating a Windows Guest VM with VHD Disk Image

This is the typical configuration that this PoC aims to cater to. There are two important constraints that this type of VM brings:

1. No `virtio` drivers in the VM image - because they are downloaded straight from the Azure Marketplace.

2. Large filesize for the VHD images.

To cater to both these constraints, this fork of KubeVirt allows users to specify that their VMI's disk needs to be passed to the VM using the `vmbus` transport instead of the default `virtio`.

Furthermore, we use `hostpath` Persistent Volumes in K8s to pass the VHD from the dev/test node through the KinD node and into the guest VM.

### 6.1. Mounting host directory containing VHD into KinD node

IMPORTANT: The VHD file should be present in a certain directory named `disk.img`.
Add the following additional extra mount to the KinD config at `kubevirtci/cluster-up/cluster/kind/common.sh`.

```yaml
  - containerPath: /disks
    hostPath: <dir-containing-vhd>
```

Then run `make cluster-up`. The resultant `kind-1.35-control-plane` node should see the `/disks` directory with VHD inside.

```bash
$ docker exec -it kind-1.35-control-plane /bin/bash -c "ls -l /disks"
total 134217768
-rw-rw-r-- 1 1001 1003 137438953984 Aug 25 02:45 disk.img
```

### Create a HostPath Persistent Volume and Persistent Volume Claim

```
apiVersion: v1
kind: PersistentVolume
metadata:
  name: local-vhd-pv
spec:
  capacity:
    storage: 150Gi
  volumeMode: Filesystem
  accessModes:
    - ReadWriteOnce
  persistentVolumeReclaimPolicy: Retain
  storageClassName: local
  hostPath:
    path: /disks     # Path in KinD node containing VHD
    type: Directory
  nodeAffinity:
    required:
      nodeSelectorTerms:
      - matchExpressions:
        - key: kubernetes.io/hostname
          operator: In
          values:
          - kind-1.35-control-plane
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: local-vhd-pvc
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 150Gi
  storageClassName: local
  volumeName: local-vhd-pv
```

### 6.3. Create a Virtual Machine Instance (VMI)

Save the following manifest into `vmi.yaml`.

```yaml
apiVersion: kubevirt.io/v1
kind: VirtualMachineInstance
metadata:
  labels:
    special: vmi-windows
  name: vmi-windows
spec:
  domain:
    firmware:
      bootloader:
        efi:
          secureBoot: false
    devices:
      disks:
      - disk:
          bus: vmbus  # to enable vmbus transport
        name: vhdrootdisk
      interfaces:
      - masquerade: {}
        name: default
        model: vmbus  # to enable vmbus transport
      rng: {}
    memory:
      guest: 1024M
    resources: {}
  networks:
  - name: default
    pod: {}
  terminationGracePeriodSeconds: 0
  volumes:
  - persistentVolumeClaim:
      claimName: local-vhd-pvc    # refer the above PVC in [6.2]
    name: vhdrootdisk
```

```bash
kubectl apply -f vmi.yaml
```

## 7. Creating a Linux Guest VM with QCOW2 Disk Image

### 7.1. Building an OS Disk Image Based on KubeVirt Upstream's Fedora Image

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

### 7.2. Building an Image Containing the Linux Kernel for Direct Boot

Obtain an AzureLinux kernel image `vmlinux.bin` and follow the following steps.

```bash
cat > Dockerfile <<'EOF'
FROM scratch
ADD --chown=107:107 vmlinux.bin /boot/
EOF

docker build -t docker.io/harshitg/kernel:test -f .
docker push docker.io/harshitg/kernel:test
```

### 7.3. Create a Virtual Machine Instance (VMI)

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
      autoattachGraphicsDevice: false
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
virtctl console <vmi-name>

# Login credentials are fedora/fedora
```

## 9. Accessing the VMI via VNC

OpenVMM runs a VNC server on a configurable TCP port and allows clients to stream data from the guest VM's graphics device (which is added to the guest by default). You can connect to the VNC of the guest using one of twp methods.

```bash
# Option 1: Directly connect using a VNC client
$ virtctl vnc <vmi-name>

# Option 2: For dev machines lacking a GUI, setup a VNC proxy
# Access the VNC proxy on the given TCP host/port from a machine with GUI
$ virtctl vnc <vmi-name> --address=0.0.0.0 --port 5900 --proxy-only
```

## 10. Inspecting the OpenVMM Process

```bash
# Exec into the virt-launcher pod created for the above VMI
kubectl exec -it pod/virt-launcher-xxxx -- /bin/bash

# In the virt-launcher pod, you can run ps -aux to find the OpenVMM process
bash-5.3# ps -aux | grep openvmm
root          54  0.0  0.0 237976 14528 pts/0    Ssl+ 19:03   0:00 /openvmm/openvmm --kernel /var/run/kubevirt/container-disks/kernel-boot/vmlinux.bin --processors 1 --memory 977M --virtio-blk file:/var/run/kubevirt/container-disks/disk_0.img,ro,pcie_port=rp0 --pcie-root-complex rc0 --pcie-root-port rc0:rp0 -c root=/dev/vda1 console=ttyS0 cgroup_no_v1=all systemd.unified_cgroup_hierarchy=1 --pcie-root-port rc0:rp2 --virtio-net pcie_port=rp2:tap:tap0 --com1 listen=/var/run/kubevirt-private/83f5250b-c0f2-472f-83f4-0364b5562b29/virt-serial0
root          59  0.2  0.0 6799688 214516 pts/0  Sl+  19:03   0:07 /openvmm/openvmm vm
root          78  0.0  0.0   3456  1804 pts/1    S+   20:01   0:00 grep openvmm
```
