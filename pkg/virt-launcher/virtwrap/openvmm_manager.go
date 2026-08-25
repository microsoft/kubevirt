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
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/google/goterm/term"
	k8sv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"

	backupv1 "kubevirt.io/api/backup/v1alpha1"
	v1 "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/log"

	containerdisk "kubevirt.io/kubevirt/pkg/container-disk"
	cmdv1 "kubevirt.io/kubevirt/pkg/handler-launcher-com/cmd/v1"
	"kubevirt.io/kubevirt/pkg/network/cache"
	netsetup "kubevirt.io/kubevirt/pkg/network/setup/launcher"
	netvmispec "kubevirt.io/kubevirt/pkg/network/vmispec"
	"kubevirt.io/kubevirt/pkg/safepath"
	"kubevirt.io/kubevirt/pkg/storage/volumepath"
	"kubevirt.io/kubevirt/pkg/unsafepath"
	cmdclient "kubevirt.io/kubevirt/pkg/virt-handler/cmd-client"
	"kubevirt.io/kubevirt/pkg/virt-launcher/virtwrap/api"
	"kubevirt.io/kubevirt/pkg/virt-launcher/virtwrap/stats"
)

const (
	openVMMBinaryPath       = "/openvmm/openvmm"
	openVMMUEFIFirmwarePath = "/openvmm/MSVM.fd"
	openVMMConsoleDir       = "/var/run/kubevirt-private"
	openVMMStderrFile       = "openvmm.stderr.log"
	// This is the address on which OpenVMM process's VNC server listens on
	openVMMVNCListenAddress = "0.0.0.0"
	// This is the address to which the VNC proxy will connect to forward VNC connections to the OpenVMM process
	// TODO: Hardcoding the target address because connecting to localhost fails due to no route to host
	// TODO: To fix, either resolve 127.0.0.1 address resolution, or automatically extract private IP of pod
	vncForwarderTargetAddress = "10.0.2.1"
	openVMMVNCPort            = "5900"
	openVMMVNCSocket          = "virt-vnc"
)

type openVMMState uint8

const (
	openVMMNotStarted openVMMState = iota
	openVMMRunning
	openVMMStopped
	openVMMFailed
)

type domainEventNotifier interface {
	SendDomainEvent(event watch.Event) error
}

type OpenVMMDomainManager struct {
	mutex sync.RWMutex

	state       openVMMState
	command     *exec.Cmd
	domain      *api.Domain
	stopPending bool
	pidDir      string
	notifier    domainEventNotifier
	events      chan<- watch.Event

	commandFactory        func(string, ...string) *exec.Cmd
	diskPath              func(int) string
	filesystemDiskPath    func(string) string
	imageVolumeDiskPath   func(int, string) (*safepath.Path, error)
	kernelPath            func(string) string
	imageVolumeKernelPath func(string) (*safepath.Path, error)
	imageVolumeEnabled    bool
	consoleDir            string
	networkSetup          func(*v1.VirtualMachineInstance, *api.Domain, *cmdv1.VirtualMachineOptions) (string, error)
	vncListener           net.Listener
	vncTargetAddress      string
	vncSocketPath         string
}

func NewOpenVMMDomainManager(pidDir string, notifier domainEventNotifier, events chan<- watch.Event, imageVolumeEnabled bool) DomainManager {
	return &OpenVMMDomainManager{
		state:                 openVMMNotStarted,
		pidDir:                pidDir,
		notifier:              notifier,
		events:                events,
		commandFactory:        exec.Command,
		diskPath:              containerdisk.GetDiskTargetPathFromLauncherView,
		filesystemDiskPath:    volumepath.Filesystem,
		imageVolumeDiskPath:   getDiskTargetPathFromImageVolumeView,
		kernelPath:            containerdisk.GetKernelBootArtifactPathFromLauncherView,
		imageVolumeKernelPath: getKernelBootArtifactPathFromImageVolumeView,
		imageVolumeEnabled:    imageVolumeEnabled,
		consoleDir:            openVMMConsoleDir,
		vncTargetAddress:      net.JoinHostPort(vncForwarderTargetAddress, openVMMVNCPort),
	}
}

func (l *OpenVMMDomainManager) SyncVMI(vmi *v1.VirtualMachineInstance, _ bool, options *cmdv1.VirtualMachineOptions) (*api.DomainSpec, error) {
	l.mutex.Lock()
	switch l.state {
	case openVMMRunning, openVMMStopped:
		spec := l.domain.Spec.DeepCopy()
		l.mutex.Unlock()
		return spec, nil
	case openVMMFailed:
		l.mutex.Unlock()
		return nil, fmt.Errorf("OpenVMM has already failed and will not be restarted")
	}

	if l.imageVolumeEnabled {
		if err := l.linkImageVolumeFilePaths(vmi); err != nil {
			l.mutex.Unlock()
			return nil, err
		}
	}

	domain, args, err := l.buildDomainAndCommand(vmi, options)
	if err != nil {
		l.mutex.Unlock()
		return nil, err
	}

	stderrPath := filepath.Join(l.consoleDir, string(vmi.UID), openVMMStderrFile)
	stderrFile, err := os.OpenFile(stderrPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		l.mutex.Unlock()
		return nil, fmt.Errorf("failed to open OpenVMM stderr log %s: %w", stderrPath, err)
	}
	pty, err := term.OpenPTY()
	if err != nil {
		_ = stderrFile.Close()
		l.mutex.Unlock()
		return nil, fmt.Errorf("failed to create OpenVMM controlling terminal: %w", err)
	}
	if openVMMGraphicsEnabled(vmi) {
		if err := l.startVNCProxyLocked(vmi.UID); err != nil {
			_ = stderrFile.Close()
			_ = pty.Close()
			l.mutex.Unlock()
			return nil, err
		}
	}

	command := l.commandFactory(openVMMBinaryPath, args...)
	command.Stdin = pty.Slave
	command.Stdout = pty.Slave
	command.Stderr = stderrFile
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	// Add logging for the OpenVMM command being executed
	log.Log.Infof("Starting OpenVMM with command: %s %v; stderr: %s", openVMMBinaryPath, args, stderrPath)
	if err := command.Start(); err != nil {
		_ = stderrFile.Close()
		_ = pty.Close()
		l.stopVNCProxyLocked()
		l.state = openVMMFailed
		l.mutex.Unlock()
		return nil, fmt.Errorf("failed to start OpenVMM: %w", err)
	}
	if err := stderrFile.Close(); err != nil {
		log.Log.Reason(err).Warningf("failed to close parent copy of OpenVMM stderr log %s", stderrPath)
	}
	if err := pty.Slave.Close(); err != nil {
		log.Log.Reason(err).Warning("failed to close parent copy of OpenVMM PTY slave")
	}
	go func() { _, _ = io.Copy(io.Discard, pty.Master) }()

	l.command = command
	l.domain = domain
	l.state = openVMMRunning
	if err := l.writePIDFileLocked(domain.Spec.Name, command.Process.Pid); err != nil {
		_ = command.Process.Kill()
		l.stopVNCProxyLocked()
		go func() {
			_ = command.Wait()
			_ = pty.Master.Close()
		}()
		l.state = openVMMFailed
		l.mutex.Unlock()
		return nil, err
	}
	spec := domain.Spec.DeepCopy()
	event := watch.Event{Type: watch.Added, Object: domain.DeepCopy()}
	l.mutex.Unlock()

	l.publish(event)
	go l.waitForExit(command, pty.Master)
	return spec, nil
}

func (l *OpenVMMDomainManager) linkImageVolumeFilePaths(vmi *v1.VirtualMachineInstance) error {
	for volumeIndex, volume := range vmi.Spec.Volumes {
		if volume.ContainerDisk == nil {
			continue
		}
		backingFile := l.diskPath(volumeIndex)
		fileToSoftLink, err := l.imageVolumeDiskPath(volumeIndex, volume.ContainerDisk.Path)
		if err != nil {
			return fmt.Errorf("failed to find disk file from ImageVolume: %v", err)
		}
		if err := os.Symlink(unsafepath.UnsafeAbsolute(fileToSoftLink.Raw()), backingFile); err != nil && !os.IsExist(err) {
			return fmt.Errorf("error creating symlink for containerDisk: %v", err)
		}
	}

	if vmi.Spec.Domain.Firmware == nil || vmi.Spec.Domain.Firmware.KernelBoot == nil || vmi.Spec.Domain.Firmware.KernelBoot.Container == nil {
		return nil
	}

	kernelBoot := vmi.Spec.Domain.Firmware.KernelBoot.Container
	if kernelBoot.KernelPath == "" {
		return nil
	}
	kernelPath := l.kernelPath(kernelBoot.KernelPath)
	if err := os.MkdirAll(filepath.Dir(kernelPath), 0755); err != nil {
		return fmt.Errorf("failed to create kernel boot artifact directory: %w", err)
	}
	fileToSoftLink, err := l.imageVolumeKernelPath(kernelBoot.KernelPath)
	if err != nil {
		return fmt.Errorf("failed to find kernel boot artifact from ImageVolume: %v", err)
	}
	if err := os.Symlink(unsafepath.UnsafeAbsolute(fileToSoftLink.Raw()), kernelPath); err != nil && !os.IsExist(err) {
		return fmt.Errorf("error creating symlink for kernel boot artifact: %v", err)
	}
	return nil
}

func (l *OpenVMMDomainManager) buildDomainAndCommand(vmi *v1.VirtualMachineInstance, options *cmdv1.VirtualMachineOptions) (*api.Domain, []string, error) {
	uefiBoot := vmi.IsBootloaderEFI()
	var kernelBoot *v1.KernelBoot
	var kernelPath string
	if !uefiBoot {
		if vmi.Spec.Domain.Firmware == nil || vmi.Spec.Domain.Firmware.KernelBoot == nil || vmi.Spec.Domain.Firmware.KernelBoot.Container == nil {
			return nil, nil, fmt.Errorf("OpenVMM requires an external kernel boot container")
		}
		kernelBoot = vmi.Spec.Domain.Firmware.KernelBoot
		if kernelBoot.Container.KernelPath == "" {
			return nil, nil, fmt.Errorf("OpenVMM requires an external kernel path")
		}
		kernelPath = l.kernelPath(kernelBoot.Container.KernelPath)
		if _, err := os.Stat(kernelPath); err != nil {
			return nil, nil, fmt.Errorf("failed to access kernel at %s: %w", kernelPath, err)
		}
	}

	volumeName, diskPath, diskBus, err := l.rootDisk(vmi)
	if err != nil {
		return nil, nil, err
	}
	if _, err := os.Stat(diskPath); err != nil {
		return nil, nil, fmt.Errorf("failed to access root disk %s at %s: %w", volumeName, diskPath, err)
	}

	topology, processorCount := openVMMCPUTopology(vmi)
	memoryMiB := openVMMMemoryMiB(vmi)

	domain := &api.Domain{
		ObjectMeta: metav1.ObjectMeta{Name: vmi.Name, Namespace: vmi.Namespace, UID: vmi.UID},
		Spec: api.DomainSpec{
			Type:   "openvmm",
			Name:   api.VMINamespaceKeyFunc(vmi),
			UUID:   string(vmi.UID),
			OS:     api.OS{Kernel: kernelPath},
			Memory: api.Memory{Value: uint64(memoryMiB), Unit: "MiB"},
			VCPU:   &api.VCPU{CPUs: uint32(processorCount)},
			CPU:    api.CPU{Topology: topology},
			Metadata: api.Metadata{KubeVirt: api.KubeVirtMetadata{
				UID: vmi.UID,
			}},
			Devices: api.Devices{Disks: []api.Disk{{
				Device:   "disk",
				Type:     "file",
				Source:   api.DiskSource{File: diskPath},
				Target:   api.DiskTarget{Device: "vda", Bus: diskBus},
				Alias:    api.NewUserDefinedAlias(volumeName),
				ReadOnly: &api.ReadOnly{},
			}}},
		},
	}
	if uefiBoot {
		domain.Spec.OS.BootLoader = &api.Loader{Path: openVMMUEFIFirmwarePath}
	} else {
		domain.Spec.OS.KernelArgs = kernelBoot.KernelArgs
	}
	domain.SetState(api.Running, api.ReasonUnknown)

	consolePath := filepath.Join(l.consoleDir, string(vmi.UID), "virt-serial0")
	if err := os.MkdirAll(filepath.Dir(consolePath), 0755); err != nil {
		return nil, nil, fmt.Errorf("failed to create OpenVMM console directory: %w", err)
	}

	args := []string{}
	if uefiBoot {
		args = append(args, "--uefi", "--uefi-firmware", openVMMUEFIFirmwarePath)
	} else {
		args = append(args, "--kernel", kernelPath)
	}
	args = append(args,
		"--processors", strconv.FormatUint(uint64(processorCount), 10),
		"--memory", fmt.Sprintf("%dM", memoryMiB),
	)
	if openVMMGraphicsEnabled(vmi) {
		args = append(args,
			"--vnc-listen", openVMMVNCListenAddress,
			"--vnc-port", openVMMVNCPort,
			"--gfx",
		)
	}
	if diskBus == v1.DiskBusVMBus {
		args = append(args,
			"--vmbus-scsi", "id=scsi0",
			"--disk", fmt.Sprintf("file:%s,on=scsi0", diskPath),
		)
	} else {
		args = append(args,
			"--virtio-blk", fmt.Sprintf("file:%s,ro,pcie_port=rp0", diskPath),
			"--pcie-root-complex", "rc0",
			"--pcie-root-port", "rc0:rp0",
		)
	}
	if !uefiBoot && kernelBoot.KernelArgs != "" {
		args = append(args, "-c", kernelBoot.KernelArgs)
	}

	if len(vmi.Spec.Domain.Devices.Interfaces) > 0 {
		setupNetwork := l.networkSetup
		if setupNetwork == nil {
			setupNetwork = l.setupNetwork
		}
		tapName, err := setupNetwork(vmi, domain, options)
		if err != nil {
			return nil, nil, err
		}
		if vmi.Spec.Domain.Devices.Interfaces[0].Model == v1.VMBus {
			args = append(args, "--net", "tap:"+tapName)
		} else {
			if diskBus == v1.DiskBusVMBus {
				args = append(args, "--pcie-root-complex", "rc0")
			}
			args = append(args,
				"--pcie-root-port", "rc0:rp2",
				"--virtio-net", fmt.Sprintf("pcie_port=rp2:tap:%s", tapName),
			)
		}
	}
	args = append(args, "--com1", "listen="+consolePath)

	return domain, args, nil
}

func openVMMGraphicsEnabled(vmi *v1.VirtualMachineInstance) bool {
	return vmi.Spec.Domain.Devices.AutoattachGraphicsDevice == nil || *vmi.Spec.Domain.Devices.AutoattachGraphicsDevice
}

func (l *OpenVMMDomainManager) startVNCProxyLocked(uid types.UID) error {
	socketPath := filepath.Join(l.consoleDir, string(uid), openVMMVNCSocket)
	if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to remove stale OpenVMM VNC socket: %w", err)
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("failed to listen on OpenVMM VNC socket %s: %w", socketPath, err)
	}
	l.vncListener = listener
	l.vncSocketPath = socketPath
	go l.serveVNCProxy(listener)
	return nil
}

func (l *OpenVMMDomainManager) serveVNCProxy(listener net.Listener) {
	for {
		client, err := listener.Accept()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				log.Log.Reason(err).Error("OpenVMM VNC proxy failed to accept a connection")
			}
			return
		}
		go l.proxyVNCConnection(client)
	}
}

func (l *OpenVMMDomainManager) proxyVNCConnection(client net.Conn) {
	defer client.Close()
	server, err := dialOpenVMMVNC(l.vncTargetAddress)
	if err != nil {
		log.Log.Reason(err).Errorf("OpenVMM VNC proxy failed to connect to %s", l.vncTargetAddress)
		return
	}
	defer server.Close()

	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(server, client)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, server)
		done <- struct{}{}
	}()
	<-done
}

func dialOpenVMMVNC(address string) (net.Conn, error) {
	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", address, 250*time.Millisecond)
		if err == nil {
			return conn, nil
		}
		if time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (l *OpenVMMDomainManager) stopVNCProxyLocked() {
	if l.vncListener != nil {
		_ = l.vncListener.Close()
		l.vncListener = nil
	}
	if l.vncSocketPath != "" {
		if err := os.Remove(l.vncSocketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Log.Reason(err).Warning("failed to remove OpenVMM VNC socket")
		}
		l.vncSocketPath = ""
	}
}

func openVMMCPUTopology(vmi *v1.VirtualMachineInstance) (*api.CPUTopology, uint32) {
	topology := &api.CPUTopology{Sockets: 1, Cores: 1, Threads: 1}
	if cpu := vmi.Spec.Domain.CPU; cpu != nil {
		if cpu.Sockets > 0 {
			topology.Sockets = cpu.Sockets
		}
		if cpu.Cores > 0 {
			topology.Cores = cpu.Cores
		}
		if cpu.Threads > 0 {
			topology.Threads = cpu.Threads
		}
		if cpu.Sockets > 0 || cpu.Cores > 0 || cpu.Threads > 0 {
			return topology, topology.Sockets * topology.Cores * topology.Threads
		}
	}

	if cpu, exists := vmi.Spec.Domain.Resources.Limits[k8sv1.ResourceCPU]; exists && cpu.Value() > 0 {
		topology.Sockets = uint32(cpu.Value())
	} else if cpu, exists := vmi.Spec.Domain.Resources.Requests[k8sv1.ResourceCPU]; exists && cpu.Value() > 0 {
		topology.Sockets = uint32(cpu.Value())
	}
	return topology, topology.Sockets
}

func openVMMMemoryMiB(vmi *v1.VirtualMachineInstance) int64 {
	const (
		mib                = int64(1024 * 1024)
		defaultMemoryBytes = int64(1024 * 1024 * 1024)
	)

	memoryBytes := defaultMemoryBytes
	if vmi.Spec.Domain.Memory != nil && vmi.Spec.Domain.Memory.Guest != nil {
		memoryBytes = vmi.Spec.Domain.Memory.Guest.Value()
	} else if memory, exists := vmi.Spec.Domain.Resources.Requests[k8sv1.ResourceMemory]; exists {
		memoryBytes = memory.Value()
	} else if memory, exists := vmi.Spec.Domain.Resources.Limits[k8sv1.ResourceMemory]; exists {
		memoryBytes = memory.Value()
	}
	if memoryBytes <= 0 {
		memoryBytes = defaultMemoryBytes
	}
	return max(int64(1), (memoryBytes+mib-1)/mib)
}

func (l *OpenVMMDomainManager) rootDisk(vmi *v1.VirtualMachineInstance) (string, string, v1.DiskBus, error) {
	if len(vmi.Spec.Domain.Devices.Disks) != 1 {
		return "", "", "", fmt.Errorf("OpenVMM PoC requires exactly one root disk")
	}
	disk := vmi.Spec.Domain.Devices.Disks[0]
	if disk.Disk == nil {
		return "", "", "", fmt.Errorf("OpenVMM PoC root disk must be a disk device")
	}
	diskBus := disk.Disk.Bus
	if diskBus == "" {
		diskBus = v1.DiskBusVirtio
	}
	if diskBus != v1.DiskBusVirtio && diskBus != v1.DiskBusVMBus {
		return "", "", "", fmt.Errorf("OpenVMM PoC root disk bus must be virtio or vmbus")
	}
	for index, volume := range vmi.Spec.Volumes {
		fmt.Printf("OpenVMM volume at index %d: %+v\n", index, volume)
		if volume.Name == disk.Name {
			switch {
			case volume.ContainerDisk != nil:
				return volume.Name, l.diskPath(index), diskBus, nil
			case volume.PersistentVolumeClaim != nil:
				return volume.Name, l.filesystemDiskPath(volume.Name), diskBus, nil
			case volume.HostDisk != nil && isPVCBacked(volume.Name, vmi):
				return volume.Name, volume.HostDisk.Path, diskBus, nil
			default:
				return "", "", "", fmt.Errorf("OpenVMM PoC root disk must be a containerDisk or filesystem persistentVolumeClaim")
			}
		}
	}
	return "", "", "", fmt.Errorf("no volume found for root disk %s", disk.Name)
}

func (l *OpenVMMDomainManager) setupNetwork(vmi *v1.VirtualMachineInstance, domain *api.Domain, options *cmdv1.VirtualMachineOptions) (string, error) {
	if len(vmi.Spec.Domain.Devices.Interfaces) != 1 || len(vmi.Spec.Networks) != 1 {
		return "", fmt.Errorf("OpenVMM PoC supports at most one network interface")
	}
	iface := vmi.Spec.Domain.Devices.Interfaces[0]
	if iface.Bridge == nil && iface.Masquerade == nil {
		return "", fmt.Errorf("OpenVMM PoC network interface must use bridge or masquerade binding")
	}
	domain.Spec.Devices.Interfaces = []api.Interface{{
		Type:  "ethernet",
		Alias: api.NewUserDefinedAlias(iface.Name),
		Model: &api.Model{Type: v1.VirtIO},
	}}
	domainAttachments := map[string]string{iface.Name: string(v1.Tap)}
	if options != nil && options.GetInterfaceDomainAttachment()[iface.Name] != "" {
		domainAttachments = options.GetInterfaceDomainAttachment()
	}
	nonAbsentIfaces := netvmispec.FilterInterfacesSpec(vmi.Spec.Domain.Devices.Interfaces, func(iface v1.Interface) bool {
		return iface.State != v1.InterfaceStateAbsent
	})
	networks := netvmispec.FilterNetworksByInterfaces(vmi.Spec.Networks, nonAbsentIfaces)
	if err := netsetup.NewVMNetworkConfigurator(vmi, cache.CacheCreator{}, netsetup.WithDomainAttachments(domainAttachments)).SetupPodNetworkPhase2(domain, networks); err != nil {
		return "", fmt.Errorf("failed to prepare OpenVMM TAP interface: %w", err)
	}
	if domain.Spec.Devices.Interfaces[0].Target == nil || domain.Spec.Devices.Interfaces[0].Target.Device == "" {
		return "", fmt.Errorf("network phase 2 did not provide a TAP device")
	}
	return domain.Spec.Devices.Interfaces[0].Target.Device, nil
}

func (l *OpenVMMDomainManager) writePIDFileLocked(domainName string, pid int) error {
	if err := os.MkdirAll(l.pidDir, 0755); err != nil {
		return fmt.Errorf("failed to create OpenVMM PID directory: %w", err)
	}
	if err := os.WriteFile(filepath.Join(l.pidDir, domainName+".pid"), []byte(strconv.Itoa(pid)), 0644); err != nil {
		return fmt.Errorf("failed to write OpenVMM PID file: %w", err)
	}
	return nil
}

func (l *OpenVMMDomainManager) waitForExit(command *exec.Cmd, ptyMaster *os.File) {
	err := command.Wait()
	if err := ptyMaster.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
		log.Log.Reason(err).Warning("failed to close OpenVMM PTY master")
	}

	l.mutex.Lock()
	if command != l.command || l.state == openVMMStopped {
		l.mutex.Unlock()
		return
	}
	reason := api.ReasonShutdown
	l.state = openVMMStopped
	if err != nil && !l.stopPending {
		// Log the reason for the failure
		stderrPath := filepath.Join(l.consoleDir, string(l.domain.UID), openVMMStderrFile)
		log.Log.Reason(err).Errorf("OpenVMM process exited with an error; inspect stderr at %s", stderrPath)
		l.state = openVMMFailed
		reason = api.ReasonCrashed
	}
	l.domain.SetState(api.Shutoff, reason)
	l.stopVNCProxyLocked()
	now := metav1.Now()
	l.domain.DeletionTimestamp = &now
	event := watch.Event{Type: watch.Modified, Object: l.domain.DeepCopy()}
	pidFile := filepath.Join(l.pidDir, l.domain.Spec.Name+".pid")
	l.mutex.Unlock()

	if removeErr := os.Remove(pidFile); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		log.Log.Reason(removeErr).Warning("failed to remove OpenVMM PID file")
	}
	l.publish(event)
}

func (l *OpenVMMDomainManager) publish(event watch.Event) {
	if l.events != nil {
		select {
		case l.events <- event:
		default:
			log.Log.Warning("dropping local OpenVMM domain event because the channel is full")
		}
	}
	if l.notifier != nil {
		if err := l.notifier.SendDomainEvent(event); err != nil {
			log.Log.Reason(err).Error("failed to publish OpenVMM domain event")
		}
	}
}

func (l *OpenVMMDomainManager) ListAllDomains() ([]*api.Domain, error) {
	l.mutex.RLock()
	defer l.mutex.RUnlock()
	if l.domain == nil {
		return nil, nil
	}
	return []*api.Domain{l.domain.DeepCopy()}, nil
}

func (l *OpenVMMDomainManager) KillVMI(_ *v1.VirtualMachineInstance) error {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	if l.command == nil || l.command.Process == nil || l.state != openVMMRunning {
		return nil
	}
	l.stopPending = true
	if err := l.command.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("failed to terminate OpenVMM: %w", err)
	}
	return nil
}

func (l *OpenVMMDomainManager) DeleteVMI(vmi *v1.VirtualMachineInstance) error {
	return l.KillVMI(vmi)
}

func (l *OpenVMMDomainManager) SignalShutdownVMI(vmi *v1.VirtualMachineInstance) error {
	return l.KillVMI(vmi)
}

func (l *OpenVMMDomainManager) PauseVMI(*v1.VirtualMachineInstance) error         { return nil }
func (l *OpenVMMDomainManager) UnpauseVMI(*v1.VirtualMachineInstance) error       { return nil }
func (l *OpenVMMDomainManager) FreezeVMI(*v1.VirtualMachineInstance, int32) error { return nil }
func (l *OpenVMMDomainManager) UnfreezeVMI(*v1.VirtualMachineInstance) error      { return nil }
func (l *OpenVMMDomainManager) ResetVMI(*v1.VirtualMachineInstance) error         { return nil }
func (l *OpenVMMDomainManager) SoftRebootVMI(*v1.VirtualMachineInstance) error    { return nil }
func (l *OpenVMMDomainManager) MarkGracefulShutdownVMI()                          {}
func (l *OpenVMMDomainManager) MigrateVMI(*v1.VirtualMachineInstance, *cmdclient.MigrationOptions) error {
	return nil
}
func (l *OpenVMMDomainManager) PrepareMigrationTarget(*v1.VirtualMachineInstance, bool, *cmdv1.VirtualMachineOptions) error {
	return nil
}
func (l *OpenVMMDomainManager) CancelVMIMigration(*v1.VirtualMachineInstance) error { return nil }
func (l *OpenVMMDomainManager) FinalizeVirtualMachineMigration(*v1.VirtualMachineInstance, *cmdv1.VirtualMachineOptions) error {
	return nil
}
func (l *OpenVMMDomainManager) HotplugHostDevices(*v1.VirtualMachineInstance) error  { return nil }
func (l *OpenVMMDomainManager) Exec(string, string, []string, int32) (string, error) { return "", nil }
func (l *OpenVMMDomainManager) GuestPing(string) error                               { return nil }
func (l *OpenVMMDomainManager) MemoryDump(*v1.VirtualMachineInstance, string) error  { return nil }
func (l *OpenVMMDomainManager) BackupVirtualMachine(*v1.VirtualMachineInstance, *backupv1.BackupOptions) error {
	return nil
}
func (l *OpenVMMDomainManager) RedefineCheckpoint(*v1.VirtualMachineInstance, *backupv1.BackupCheckpoint) (bool, error) {
	return false, nil
}
func (l *OpenVMMDomainManager) UpdateVCPUs(*v1.VirtualMachineInstance, *cmdv1.VirtualMachineOptions) error {
	return nil
}
func (l *OpenVMMDomainManager) UpdateGuestMemory(*v1.VirtualMachineInstance) error { return nil }
func (l *OpenVMMDomainManager) InjectLaunchSecret(*v1.VirtualMachineInstance, *v1.SEVSecretOptions) error {
	return nil
}
func (l *OpenVMMDomainManager) InterfacesStatus() []api.InterfaceStatus { return nil }
func (l *OpenVMMDomainManager) GetGuestInfo() v1.VirtualMachineInstanceGuestAgentInfo {
	return v1.VirtualMachineInstanceGuestAgentInfo{}
}
func (l *OpenVMMDomainManager) GetUsers() []v1.VirtualMachineInstanceGuestOSUser      { return nil }
func (l *OpenVMMDomainManager) GetFilesystems() []v1.VirtualMachineInstanceFileSystem { return nil }
func (l *OpenVMMDomainManager) GetGuestOSInfo() *api.GuestOSInfo                      { return nil }
func (l *OpenVMMDomainManager) GetQemuVersion() (string, error)                       { return "openvmm", nil }
func (l *OpenVMMDomainManager) GetSEVInfo() (*v1.SEVPlatformInfo, error)              { return nil, nil }
func (l *OpenVMMDomainManager) GetLaunchMeasurement(*v1.VirtualMachineInstance) (*v1.SEVMeasurementInfo, error) {
	return nil, nil
}
func (l *OpenVMMDomainManager) GetScreenshot(*v1.VirtualMachineInstance) (*cmdv1.ScreenshotResponse, error) {
	return nil, nil
}
func (l *OpenVMMDomainManager) GetGuestAgentVersion() string        { return "" }
func (l *OpenVMMDomainManager) GetAgentData(string) (string, error) { return "", nil }

func (l *OpenVMMDomainManager) GetDomainStats() (*stats.DomainStats, error) {
	l.mutex.RLock()
	defer l.mutex.RUnlock()
	result := &stats.DomainStats{}
	if l.domain != nil {
		result.Name = l.domain.Spec.Name
		result.UUID = l.domain.Spec.UUID
	}
	return result, nil
}

func (l *OpenVMMDomainManager) GetDomainDirtyRateStats(time.Duration) (*stats.DomainStatsDirtyRate, error) {
	return nil, nil
}

var _ DomainManager = (*OpenVMMDomainManager)(nil)
