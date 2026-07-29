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
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"

	v1 "kubevirt.io/api/core/v1"

	cmdv1 "kubevirt.io/kubevirt/pkg/handler-launcher-com/cmd/v1"
	"kubevirt.io/kubevirt/pkg/libvmi"
	"kubevirt.io/kubevirt/pkg/virt-launcher/virtwrap/api"
)

var _ = Describe("OpenVMM manager", func() {
	newVMI := func() *v1.VirtualMachineInstance {
		vmi := libvmi.New(
			libvmi.WithContainerDisk("root", "example.invalid/root:latest"),
			libvmi.WithCPUCount(2, 1, 1),
			libvmi.WithMemoryRequest("512Mi"),
		)
		vmi.Name = "testvmi"
		vmi.Namespace = "testnamespace"
		vmi.UID = types.UID("test-uid")
		return vmi
	}

	newManager := func(tempDir string) (*OpenVMMDomainManager, string) {
		diskPath := filepath.Join(tempDir, "disk.raw")
		Expect(os.WriteFile(diskPath, []byte("disk"), 0600)).To(Succeed())
		manager := NewOpenVMMDomainManager(filepath.Join(tempDir, "pids"), nil, nil).(*OpenVMMDomainManager)
		manager.diskPath = func(int) string { return diskPath }
		manager.consoleDir = filepath.Join(tempDir, "console")
		return manager, diskPath
	}

	It("builds the basic OpenVMM command without invoking the converter", func() {
		tempDir := GinkgoT().TempDir()
		manager, diskPath := newManager(tempDir)

		domain, args, err := manager.buildDomainAndCommand(newVMI(), nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(domain.Spec.UUID).To(Equal("test-uid"))
		Expect(args).To(Equal([]string{
			"--kernel", openVMMKernelPath,
			"--processors", "2",
			"--memory", "512M",
			"--virtio-blk", "file:" + diskPath + ",ro,pcie_port=rp0",
			"-c", openVMMKernelArgs,
			"--pcie-root-complex", "rc0",
			"--pcie-root-port", "rc0:rp0",
			"--com1", "listen=" + filepath.Join(tempDir, "console", "test-uid", "virt-serial0"),
		}))
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
