/*
 * This file is part of the KubeVirt project
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * Copyright The KubeVirt Authors.
 *
 */

package virtwrap

import (
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	k8sv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"

	v1 "kubevirt.io/api/core/v1"

	cmdv1 "kubevirt.io/kubevirt/pkg/handler-launcher-com/cmd/v1"
	hostdisk "kubevirt.io/kubevirt/pkg/host-disk"
	"kubevirt.io/kubevirt/pkg/libvmi"
	"kubevirt.io/kubevirt/pkg/safepath"
	"kubevirt.io/kubevirt/pkg/storage/volumepath"
	"kubevirt.io/kubevirt/pkg/virt-launcher/virtwrap/api"
)

var _ = Describe("OpenVMM manager", func() {
	newVMI := func() *v1.VirtualMachineInstance {
		vmi := libvmi.New(
			libvmi.WithContainerDisk("root", "example.invalid/root:latest"),
			libvmi.WithCPUCount(2, 1, 1),
			libvmi.WithMemoryRequest("512Mi"),
			libvmi.WithKernelBoot("example.invalid/kernel:latest", "/boot/vmlinuz", "", "root=/dev/vda1 console=ttyS0"),
		)
		vmi.Name = "testvmi"
		vmi.Namespace = "testnamespace"
		vmi.UID = types.UID("test-uid")
		return vmi
	}
	newPVCVMI := func() *v1.VirtualMachineInstance {
		vmi := newVMI()
		vmi.Spec.Volumes[0].VolumeSource = v1.VolumeSource{
			PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{
				PersistentVolumeClaimVolumeSource: k8sv1.PersistentVolumeClaimVolumeSource{ClaimName: "root-pvc"},
			},
		}
		return vmi
	}

	newManager := func(tempDir string) (*OpenVMMDomainManager, string) {
		diskPath := filepath.Join(tempDir, "disk.raw")
		Expect(os.WriteFile(diskPath, []byte("disk"), 0600)).To(Succeed())
		kernelPath := filepath.Join(tempDir, "kernel-boot", "vmlinuz")
		Expect(os.MkdirAll(filepath.Dir(kernelPath), 0755)).To(Succeed())
		Expect(os.WriteFile(kernelPath, []byte("kernel"), 0600)).To(Succeed())
		manager := NewOpenVMMDomainManager(filepath.Join(tempDir, "pids"), nil, nil, false).(*OpenVMMDomainManager)
		manager.diskPath = func(int) string { return diskPath }
		manager.kernelPath = func(string) string { return kernelPath }
		manager.consoleDir = filepath.Join(tempDir, "console")
		return manager, diskPath
	}

	It("builds the basic OpenVMM command without invoking the converter", func() {
		tempDir := GinkgoT().TempDir()
		manager, diskPath := newManager(tempDir)

		domain, args, err := manager.buildDomainAndCommand(newVMI(), nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(domain.Spec.UUID).To(Equal("test-uid"))
		Expect(domain.Spec.OS.Kernel).To(Equal(manager.kernelPath("/boot/vmlinuz")))
		Expect(domain.Spec.OS.KernelArgs).To(Equal("root=/dev/vda1 console=ttyS0"))
		Expect(args).To(Equal([]string{
			"--kernel", manager.kernelPath("/boot/vmlinuz"),
			"--processors", "2",
			"--memory", "512M",
			"--vnc-listen", "127.0.0.1",
			"--vnc-port", "5900",
			"--virtio-blk", "file:" + diskPath + ",ro,pcie_port=rp0",
			"--pcie-root-complex", "rc0",
			"--pcie-root-port", "rc0:rp0",
			"-c", "root=/dev/vda1 console=ttyS0",
			"--com1", "listen=" + filepath.Join(tempDir, "console", "test-uid", "virt-serial0"),
		}))
	})

	It("resolves filesystem PVCs through the canonical KubeVirt disk path", func() {
		manager := NewOpenVMMDomainManager("", nil, nil, false).(*OpenVMMDomainManager)

		volumeName, diskPath, _, err := manager.rootDisk(newPVCVMI())
		Expect(err).ToNot(HaveOccurred())
		Expect(volumeName).To(Equal("root"))
		Expect(diskPath).To(Equal(volumepath.Filesystem("root")))
	})

	It("resolves a filesystem PVC after virt-handler replaces it with a HostDisk", func() {
		manager := NewOpenVMMDomainManager("", nil, nil, false).(*OpenVMMDomainManager)
		vmi := newPVCVMI()
		storage := resource.MustParse("1Gi")
		volumeMode := k8sv1.PersistentVolumeFilesystem
		vmi.Status.VolumeStatus = []v1.VolumeStatus{{
			Name: "root",
			PersistentVolumeClaimInfo: &v1.PersistentVolumeClaimInfo{
				VolumeMode: &volumeMode,
				Capacity:   k8sv1.ResourceList{k8sv1.ResourceStorage: storage},
				Requests:   k8sv1.ResourceList{k8sv1.ResourceStorage: storage},
			},
		}}
		Expect(hostdisk.ReplacePVCByHostDisk(vmi)).To(Succeed())
		Expect(vmi.Spec.Volumes[0].PersistentVolumeClaim).To(BeNil())
		Expect(vmi.Spec.Volumes[0].HostDisk).ToNot(BeNil())

		volumeName, diskPath, _, err := manager.rootDisk(vmi)
		Expect(err).ToNot(HaveOccurred())
		Expect(volumeName).To(Equal("root"))
		Expect(diskPath).To(Equal(volumepath.Filesystem("root")))
	})

	It("rejects a HostDisk that is not PVC-backed", func() {
		manager := NewOpenVMMDomainManager("", nil, nil, false).(*OpenVMMDomainManager)
		vmi := newVMI()
		vmi.Spec.Volumes[0].VolumeSource = v1.VolumeSource{
			HostDisk: &v1.HostDisk{Path: "/host/disk.img", Type: v1.HostDiskExists},
		}

		_, _, _, err := manager.rootDisk(vmi)
		Expect(err).To(MatchError("OpenVMM PoC root disk must be a containerDisk or filesystem persistentVolumeClaim"))
	})

	It("uses a filesystem PVC as a virtio-blk root disk", func() {
		tempDir := GinkgoT().TempDir()
		manager, _ := newManager(tempDir)
		pvcDiskPath := filepath.Join(tempDir, "root-pvc", "disk.img")
		Expect(os.MkdirAll(filepath.Dir(pvcDiskPath), 0755)).To(Succeed())
		Expect(os.WriteFile(pvcDiskPath, []byte("disk"), 0600)).To(Succeed())
		manager.filesystemDiskPath = func(string) string { return pvcDiskPath }

		domain, args, err := manager.buildDomainAndCommand(newPVCVMI(), nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(domain.Spec.Devices.Disks[0].Source.File).To(Equal(pvcDiskPath))
		Expect(args).To(ContainElements(
			"--virtio-blk", "file:"+pvcDiskPath+",ro,pcie_port=rp0",
		))
	})

	It("passes a VHD filesystem PVC to VMBus SCSI through disk.img", func() {
		tempDir := GinkgoT().TempDir()
		manager, _ := newManager(tempDir)
		pvcDiskPath := filepath.Join(tempDir, "root-pvc", "disk.img")
		Expect(os.MkdirAll(filepath.Dir(pvcDiskPath), 0755)).To(Succeed())
		Expect(os.WriteFile(pvcDiskPath, []byte("vhd image"), 0600)).To(Succeed())
		manager.filesystemDiskPath = func(string) string { return pvcDiskPath }
		vmi := newPVCVMI()
		vmi.Spec.Domain.Devices.Disks[0].Disk.Bus = v1.DiskBusVMBus

		domain, args, err := manager.buildDomainAndCommand(vmi, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(domain.Spec.Devices.Disks[0].Source.File).To(Equal(pvcDiskPath))
		Expect(args).To(ContainElements(
			"--vmbus-scsi", "id=scsi0",
			"--disk", "file:"+pvcDiskPath+",on=scsi0",
		))
	})

	It("rejects unsupported root volume sources", func() {
		manager, _ := newManager(GinkgoT().TempDir())
		vmi := newVMI()
		vmi.Spec.Volumes[0].VolumeSource = v1.VolumeSource{}

		_, _, err := manager.buildDomainAndCommand(vmi, nil)
		Expect(err).To(MatchError("OpenVMM PoC root disk must be a containerDisk or filesystem persistentVolumeClaim"))
	})

	It("uses virtio-blk when the disk bus is unspecified", func() {
		manager, _ := newManager(GinkgoT().TempDir())
		vmi := newVMI()
		vmi.Spec.Domain.Devices.Disks[0].Disk.Bus = ""

		domain, args, err := manager.buildDomainAndCommand(vmi, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(domain.Spec.Devices.Disks[0].Target.Bus).To(Equal(v1.DiskBusVirtio))
		Expect(args).To(ContainElement("--virtio-blk"))
		Expect(args).ToNot(ContainElement("--vmbus-scsi"))
	})

	It("uses VMBus SCSI when the disk bus is vmbus", func() {
		manager, diskPath := newManager(GinkgoT().TempDir())
		vmi := newVMI()
		vmi.Spec.Domain.Devices.Disks[0].Disk.Bus = v1.DiskBusVMBus

		domain, args, err := manager.buildDomainAndCommand(vmi, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(domain.Spec.Devices.Disks[0].Target.Bus).To(Equal(v1.DiskBusVMBus))
		Expect(args).To(ContainElements(
			"--vmbus-scsi", "id=scsi0",
			"--disk", "file:"+diskPath+",on=scsi0",
		))
		Expect(args).ToNot(ContainElement("--virtio-blk"))
		Expect(args).ToNot(ContainElement("rc0:rp0"))
	})

	It("builds an OpenVMM UEFI command without requiring a kernel", func() {
		tempDir := GinkgoT().TempDir()
		manager, diskPath := newManager(tempDir)
		vmi := newVMI()
		vmi.Spec.Domain.Firmware.KernelBoot = nil
		vmi.Spec.Domain.Firmware.Bootloader = &v1.Bootloader{EFI: &v1.EFI{}}

		domain, args, err := manager.buildDomainAndCommand(vmi, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(domain.Spec.OS.Kernel).To(BeEmpty())
		Expect(domain.Spec.OS.KernelArgs).To(BeEmpty())
		Expect(domain.Spec.OS.BootLoader).To(Equal(&api.Loader{Path: openVMMUEFIFirmwarePath}))
		Expect(args).To(Equal([]string{
			"--uefi",
			"--uefi-firmware", openVMMUEFIFirmwarePath,
			"--processors", "2",
			"--memory", "512M",
			"--vnc-listen", "127.0.0.1",
			"--vnc-port", "5900",
			"--virtio-blk", "file:" + diskPath + ",ro,pcie_port=rp0",
			"--pcie-root-complex", "rc0",
			"--pcie-root-port", "rc0:rp0",
			"--com1", "listen=" + filepath.Join(tempDir, "console", "test-uid", "virt-serial0"),
		}))
	})

	It("disables the OpenVMM VNC server when graphics auto-attachment is disabled", func() {
		manager, _ := newManager(GinkgoT().TempDir())
		vmi := newVMI()
		autoattachGraphics := false
		vmi.Spec.Domain.Devices.AutoattachGraphicsDevice = &autoattachGraphics

		_, args, err := manager.buildDomainAndCommand(vmi, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(args).ToNot(ContainElements("--vnc-listen", "--vnc-port"))
	})

	It("proxies the VNC unix socket to the OpenVMM TCP listener", func() {
		tempDir := GinkgoT().TempDir()
		manager, _ := newManager(tempDir)
		backend, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		defer backend.Close()
		manager.vncTargetAddress = backend.Addr().String()

		go func() {
			conn, acceptErr := backend.Accept()
			if acceptErr != nil {
				return
			}
			defer conn.Close()
			_, _ = io.Copy(conn, conn)
		}()

		vncDir := filepath.Join(tempDir, "console", "test-uid")
		Expect(os.MkdirAll(vncDir, 0755)).To(Succeed())
		Expect(manager.startVNCProxyLocked(types.UID("test-uid"))).To(Succeed())
		defer manager.stopVNCProxyLocked()

		client, err := net.Dial("unix", filepath.Join(vncDir, openVMMVNCSocket))
		Expect(err).ToNot(HaveOccurred())
		defer client.Close()
		Expect(client.SetDeadline(time.Now().Add(time.Second))).To(Succeed())
		Expect(client.Write([]byte("RFB test"))).To(Equal(8))
		response := make([]byte, 8)
		_, err = io.ReadFull(client, response)
		Expect(err).ToNot(HaveOccurred())
		Expect(response).To(Equal([]byte("RFB test")))
	})
	It("rejects a VMI without an external kernel path", func() {
		manager, _ := newManager(GinkgoT().TempDir())
		vmi := newVMI()
		vmi.Spec.Domain.Firmware.KernelBoot.Container.KernelPath = ""

		_, _, err := manager.buildDomainAndCommand(vmi, nil)
		Expect(err).To(MatchError("OpenVMM requires an external kernel path"))
	})

	It("rejects a VMI without external kernel boot configuration", func() {
		manager, _ := newManager(GinkgoT().TempDir())
		vmi := newVMI()
		vmi.Spec.Domain.Firmware.KernelBoot = nil

		_, _, err := manager.buildDomainAndCommand(vmi, nil)
		Expect(err).To(MatchError("OpenVMM requires an external kernel boot container"))
	})

	It("rejects a VMI without an external kernel boot container", func() {
		manager, _ := newManager(GinkgoT().TempDir())
		vmi := newVMI()
		vmi.Spec.Domain.Firmware.KernelBoot.Container = nil

		_, _, err := manager.buildDomainAndCommand(vmi, nil)
		Expect(err).To(MatchError("OpenVMM requires an external kernel boot container"))
	})

	It("omits empty kernel arguments", func() {
		manager, _ := newManager(GinkgoT().TempDir())
		vmi := newVMI()
		vmi.Spec.Domain.Firmware.KernelBoot.KernelArgs = ""

		domain, args, err := manager.buildDomainAndCommand(vmi, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(domain.Spec.OS.KernelArgs).To(BeEmpty())
		Expect(args).ToNot(ContainElement("-c"))
	})

	It("places a TAP-backed virtio-net device on rp2", func() {
		tempDir := GinkgoT().TempDir()
		manager, _ := newManager(tempDir)
		vmi := newVMI()
		vmi.Spec.Domain.Devices.Interfaces = []v1.Interface{{Name: "default", InterfaceBindingMethod: v1.InterfaceBindingMethod{Bridge: &v1.InterfaceBridge{}}}}
		vmi.Spec.Networks = []v1.Network{{Name: "default", NetworkSource: v1.NetworkSource{Pod: &v1.PodNetwork{}}}}
		manager.networkSetup = func(*v1.VirtualMachineInstance, *api.Domain, *cmdv1.VirtualMachineOptions) (string, error) {
			return "tap0", nil
		}

		_, args, err := manager.buildDomainAndCommand(vmi, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(args).To(ContainElements(
			"rc0:rp2",
			"pcie_port=rp2:tap:tap0",
		))
	})

	It("creates a PCIe root complex for virtio-net with a VMBus disk", func() {
		manager, _ := newManager(GinkgoT().TempDir())
		vmi := newVMI()
		vmi.Spec.Domain.Devices.Disks[0].Disk.Bus = v1.DiskBusVMBus
		vmi.Spec.Domain.Devices.Interfaces = []v1.Interface{{Name: "default", InterfaceBindingMethod: v1.InterfaceBindingMethod{Bridge: &v1.InterfaceBridge{}}}}
		vmi.Spec.Networks = []v1.Network{{Name: "default", NetworkSource: v1.NetworkSource{Pod: &v1.PodNetwork{}}}}
		manager.networkSetup = func(*v1.VirtualMachineInstance, *api.Domain, *cmdv1.VirtualMachineOptions) (string, error) {
			return "tap0", nil
		}

		_, args, err := manager.buildDomainAndCommand(vmi, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(args).To(ContainElements(
			"--pcie-root-complex", "rc0",
			"--pcie-root-port", "rc0:rp2",
			"--virtio-net", "pcie_port=rp2:tap:tap0",
		))
		Expect(args).ToNot(ContainElement("rc0:rp0"))
	})

	It("uses a TAP-backed VMBus network without PCIe arguments", func() {
		manager, _ := newManager(GinkgoT().TempDir())
		vmi := newVMI()
		vmi.Spec.Domain.Devices.Disks[0].Disk.Bus = v1.DiskBusVMBus
		vmi.Spec.Domain.Devices.Interfaces = []v1.Interface{{
			Name:                   "default",
			Model:                  v1.VMBus,
			InterfaceBindingMethod: v1.InterfaceBindingMethod{Bridge: &v1.InterfaceBridge{}},
		}}
		vmi.Spec.Networks = []v1.Network{{Name: "default", NetworkSource: v1.NetworkSource{Pod: &v1.PodNetwork{}}}}
		manager.networkSetup = func(*v1.VirtualMachineInstance, *api.Domain, *cmdv1.VirtualMachineOptions) (string, error) {
			return "tap0", nil
		}

		_, args, err := manager.buildDomainAndCommand(vmi, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(args).To(ContainElements("--net", "tap:tap0"))
		Expect(args).ToNot(ContainElement("--virtio-net"))
		Expect(args).ToNot(ContainElement("--pcie-root-port"))
		Expect(args).ToNot(ContainElement("--pcie-root-complex"))
	})

	It("links an ImageVolume disk before resolving the root disk", func() {
		tempDir := GinkgoT().TempDir()
		manager, diskPath := newManager(tempDir)
		Expect(os.Remove(diskPath)).To(Succeed())
		imageVolumeDisk := filepath.Join(tempDir, "provisioned-image.qcow2")
		Expect(os.WriteFile(imageVolumeDisk, []byte("disk"), 0600)).To(Succeed())
		manager.imageVolumeEnabled = true
		manager.imageVolumeDiskPath = func(int, string) (*safepath.Path, error) {
			return safepath.JoinAndResolveWithRelativeRoot("/", imageVolumeDisk)
		}
		manager.imageVolumeKernelPath = func(string) (*safepath.Path, error) {
			return safepath.JoinAndResolveWithRelativeRoot("/", manager.kernelPath("/boot/vmlinuz"))
		}
		manager.commandFactory = func(string, ...string) *exec.Cmd {
			return exec.Command("/bin/sh", "-c", "exit 0")
		}

		_, err := manager.SyncVMI(newVMI(), false, nil)
		Expect(err).ToNot(HaveOccurred())
		linkedDisk, err := os.Readlink(diskPath)
		Expect(err).ToNot(HaveOccurred())
		Expect(linkedDisk).To(Equal(imageVolumeDisk))
	})

	It("links an ImageVolume kernel before building the command", func() {
		tempDir := GinkgoT().TempDir()
		manager, diskPath := newManager(tempDir)
		kernelPath := manager.kernelPath("/boot/vmlinuz")
		Expect(os.Remove(kernelPath)).To(Succeed())
		imageVolumeKernel := filepath.Join(tempDir, "image-volume-vmlinuz")
		Expect(os.WriteFile(imageVolumeKernel, []byte("kernel"), 0600)).To(Succeed())
		manager.imageVolumeEnabled = true
		manager.imageVolumeDiskPath = func(int, string) (*safepath.Path, error) {
			return safepath.JoinAndResolveWithRelativeRoot("/", diskPath)
		}
		manager.imageVolumeKernelPath = func(string) (*safepath.Path, error) {
			return safepath.JoinAndResolveWithRelativeRoot("/", imageVolumeKernel)
		}
		manager.commandFactory = func(string, ...string) *exec.Cmd {
			return exec.Command("/bin/sh", "-c", "exit 0")
		}

		_, err := manager.SyncVMI(newVMI(), false, nil)
		Expect(err).ToNot(HaveOccurred())
		linkedKernel, err := os.Readlink(kernelPath)
		Expect(err).ToNot(HaveOccurred())
		Expect(linkedKernel).To(Equal(imageVolumeKernel))
	})

	It("persists OpenVMM stderr for later inspection", func() {
		tempDir := GinkgoT().TempDir()
		manager, _ := newManager(tempDir)
		manager.commandFactory = func(string, ...string) *exec.Cmd {
			return exec.Command("/bin/sh", "-c", "echo openvmm-failure >&2; exit 1")
		}

		_, err := manager.SyncVMI(newVMI(), false, nil)
		Expect(err).ToNot(HaveOccurred())
		stderrPath := filepath.Join(tempDir, "console", "test-uid", openVMMStderrFile)
		Eventually(func() string {
			contents, _ := os.ReadFile(stderrPath)
			return string(contents)
		}).Should(Equal("openvmm-failure\n"))
	})

	It("provides OpenVMM with a controlling terminal", func() {
		tempDir := GinkgoT().TempDir()
		manager, _ := newManager(tempDir)
		manager.commandFactory = func(string, ...string) *exec.Cmd {
			return exec.Command("/bin/sh", "-c", "test -t 0 && test -t 1 || { echo missing-tty >&2; exit 1; }")
		}

		_, err := manager.SyncVMI(newVMI(), false, nil)
		Expect(err).ToNot(HaveOccurred())
		stderrPath := filepath.Join(tempDir, "console", "test-uid", openVMMStderrFile)
		Consistently(func() string {
			contents, _ := os.ReadFile(stderrPath)
			return string(contents)
		}).Should(BeEmpty())
	})

	It("starts OpenVMM only once across concurrent SyncVMI calls", func() {
		tempDir := GinkgoT().TempDir()
		manager, _ := newManager(tempDir)
		var starts atomic.Int32
		manager.commandFactory = func(string, ...string) *exec.Cmd {
			starts.Add(1)
			return exec.Command("/bin/sh", "-c", "exit 0")
		}

		vmi := newVMI()
		const syncCount = 8
		var waitGroup sync.WaitGroup
		errors := make(chan error, syncCount)
		for range syncCount {
			waitGroup.Add(1)
			go func() {
				defer waitGroup.Done()
				_, err := manager.SyncVMI(vmi, false, nil)
				errors <- err
			}()
		}
		waitGroup.Wait()
		close(errors)
		for err := range errors {
			Expect(err).ToNot(HaveOccurred())
		}
		Expect(starts.Load()).To(Equal(int32(1)))
	})
})
