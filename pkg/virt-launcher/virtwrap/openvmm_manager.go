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
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	k8sv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"

	backupv1 "kubevirt.io/api/backup/v1alpha1"
	v1 "kubevirt.io/api/core/v1"
	"kubevirt.io/client-go/log"

	containerdisk "kubevirt.io/kubevirt/pkg/container-disk"
	cmdv1 "kubevirt.io/kubevirt/pkg/handler-launcher-com/cmd/v1"
	"kubevirt.io/kubevirt/pkg/network/cache"
	netsetup "kubevirt.io/kubevirt/pkg/network/setup/launcher"
	netvmispec "kubevirt.io/kubevirt/pkg/network/vmispec"
	cmdclient "kubevirt.io/kubevirt/pkg/virt-handler/cmd-client"
	"kubevirt.io/kubevirt/pkg/virt-launcher/virtwrap/api"
	"kubevirt.io/kubevirt/pkg/virt-launcher/virtwrap/stats"
)

const (
	openVMMBinaryPath = "/openvmm/openvmm"
	openVMMKernelPath = "/openvmm/vmlinux.bin"
	openVMMKernelArgs = "root=/dev/vda1 console=ttyS0 cgroup_no_v1=all systemd.unified_cgroup_hierarchy=1"
	openVMMConsoleDir = "/var/run/kubevirt-private"
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

	commandFactory func(string, ...string) *exec.Cmd
	diskPath       func(int) string
	consoleDir     string
	networkSetup   func(*v1.VirtualMachineInstance, *api.Domain, *cmdv1.VirtualMachineOptions) (string, error)
}

func NewOpenVMMDomainManager(pidDir string, notifier domainEventNotifier, events chan<- watch.Event) DomainManager {
	return &OpenVMMDomainManager{
		state:          openVMMNotStarted,
		pidDir:         pidDir,
		notifier:       notifier,
		events:         events,
		commandFactory: exec.Command,
		diskPath:       containerdisk.GetDiskTargetPathFromLauncherView,
		consoleDir:     openVMMConsoleDir,
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

	domain, args, err := l.buildDomainAndCommand(vmi, options)
	if err != nil {
		l.mutex.Unlock()
		return nil, err
	}

	command := l.commandFactory(openVMMBinaryPath, args...)
	if err := command.Start(); err != nil {
		l.state = openVMMFailed
		l.mutex.Unlock()
		return nil, fmt.Errorf("failed to start OpenVMM: %w", err)
	}

	l.command = command
	l.domain = domain
	l.state = openVMMRunning
	if err := l.writePIDFileLocked(domain.Spec.Name, command.Process.Pid); err != nil {
		_ = command.Process.Kill()
		go func() { _ = command.Wait() }()
		l.state = openVMMFailed
		l.mutex.Unlock()
		return nil, err
	}
	spec := domain.Spec.DeepCopy()
	event := watch.Event{Type: watch.Added, Object: domain.DeepCopy()}
	l.mutex.Unlock()

	l.publish(event)
	go l.waitForExit(command)
	return spec, nil
}

func (l *OpenVMMDomainManager) buildDomainAndCommand(vmi *v1.VirtualMachineInstance, options *cmdv1.VirtualMachineOptions) (*api.Domain, []string, error) {
	volumeName, diskPath, err := l.rootDisk(vmi)
	if err != nil {
		return nil, nil, err
	}
	if _, err := os.Stat(diskPath); err != nil {
		return nil, nil, fmt.Errorf("failed to access containerDisk %s at %s: %w", volumeName, diskPath, err)
	}

	topology, processorCount := openVMMCPUTopology(vmi)
	memoryMiB := openVMMMemoryMiB(vmi)

	domain := &api.Domain{
		ObjectMeta: metav1.ObjectMeta{Name: vmi.Name, Namespace: vmi.Namespace, UID: vmi.UID},
		Spec: api.DomainSpec{
			Type:   "openvmm",
			Name:   api.VMINamespaceKeyFunc(vmi),
			UUID:   string(vmi.UID),
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
				Target:   api.DiskTarget{Device: "vda", Bus: v1.DiskBusVirtio},
				Alias:    api.NewUserDefinedAlias(volumeName),
				ReadOnly: &api.ReadOnly{},
			}}},
		},
	}
	domain.SetState(api.Running, api.ReasonUnknown)

	consolePath := filepath.Join(l.consoleDir, string(vmi.UID), "virt-serial0")
	if err := os.MkdirAll(filepath.Dir(consolePath), 0755); err != nil {
		return nil, nil, fmt.Errorf("failed to create OpenVMM console directory: %w", err)
	}

	args := []string{
		"--kernel", openVMMKernelPath,
		"--processors", strconv.FormatUint(uint64(processorCount), 10),
		"--memory", fmt.Sprintf("%dM", memoryMiB),
		"--virtio-blk", fmt.Sprintf("file:%s,ro,pcie_port=rp0", diskPath),
		"-c", openVMMKernelArgs,
		"--pcie-root-complex", "rc0",
		"--pcie-root-port", "rc0:rp0",
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
		args = append(args,
			"--pcie-root-port", "rc0:rp2",
			"--virtio-net", fmt.Sprintf("pcie_port=rp2:tap:%s", tapName),
		)
	}
	args = append(args, "--com1", "listen="+consolePath)

	return domain, args, nil
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

func (l *OpenVMMDomainManager) rootDisk(vmi *v1.VirtualMachineInstance) (string, string, error) {
	if len(vmi.Spec.Domain.Devices.Disks) != 1 {
		return "", "", fmt.Errorf("OpenVMM PoC requires exactly one root disk")
	}
	disk := vmi.Spec.Domain.Devices.Disks[0]
	if disk.Disk == nil || (disk.Disk.Bus != "" && disk.Disk.Bus != v1.DiskBusVirtio) {
		return "", "", fmt.Errorf("OpenVMM PoC root disk must use virtio-blk")
	}
	for index, volume := range vmi.Spec.Volumes {
		if volume.Name == disk.Name {
			if volume.ContainerDisk == nil {
				return "", "", fmt.Errorf("OpenVMM PoC root disk must be a containerDisk")
			}
			return volume.Name, l.diskPath(index), nil
		}
	}
	return "", "", fmt.Errorf("no volume found for root disk %s", disk.Name)
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

func (l *OpenVMMDomainManager) waitForExit(command *exec.Cmd) {
	err := command.Wait()

	l.mutex.Lock()
	if command != l.command || l.state == openVMMStopped {
		l.mutex.Unlock()
		return
	}
	reason := api.ReasonShutdown
	l.state = openVMMStopped
	if err != nil && !l.stopPending {
		l.state = openVMMFailed
		reason = api.ReasonCrashed
	}
	l.domain.SetState(api.Shutoff, reason)
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
