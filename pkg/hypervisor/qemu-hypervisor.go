package hypervisor

import "regexp"

var QemuProcessExecutablePrefixes []string = []string{"qemu-system", "qemu-kvm", "cloud-hypervisor"}

// Define QemuHypervisor struct that implements the Hypervisor interface
type QemuHypervisor struct {
}

// Implement SupportsMemoryBallooning method for QemuHypervisor
func (q *QemuHypervisor) SupportsMemoryBallooning() bool {
	return true
}

// Implement RequiresBoot order method for QemuHypervisor
func (q *QemuHypervisor) RequiresBootOrder() bool {
	return false
}

// Implement GetDiskDriver method for QemuHypervisor
func (q *QemuHypervisor) GetDiskDriver() string {
	return "qemu"
}

// Implement GetLibvirtSocketPath method for QemuHypervisor
func (q *QemuHypervisor) GetLibvirtSocketPath() string {
	return "libvirt/libvirt-sock"
}

// Implement GetVcpuRegex method for QemuHypervisor
func (q *QemuHypervisor) GetVcpuRegex() *regexp.Regexp {
	// parse thread comm value expression
	return regexp.MustCompile(`^CPU (\d+)/KVM\n$`) // These threads follow this naming pattern as their command value (/proc/{pid}/task/{taskid}/comm)
	// QEMU uses threads to represent vCPUs.
}

// Implement ShouldRunPrivileged method for QemuHypervisor
func (q *QemuHypervisor) ShouldRunPrivileged() bool {
	return false
}

// Implement GetHypervisorDevice method for QemuHypervisor
func (q *QemuHypervisor) GetHypervisorDevice() string {
	return "devices.kubevirt.io/kvm"
}

// Implement GetVirtLauncherMonitorOverhead method for QemuHypervisor
func (q *QemuHypervisor) GetVirtLauncherMonitorOverhead() string {
	return "25Mi"
}

// Implement GetVirtLauncherOverhead method for QemuHypervisor
func (q *QemuHypervisor) GetVirtLauncherOverhead() string {
	return "100Mi"
}

// Implement GetVirtlogdOverhead method for QemuHypervisor
func (q *QemuHypervisor) GetVirtlogdOverhead() string {
	return "20Mi"
}

// Implement GetHypervisorDaemonOverhead method for QemuHypervisor
func (q *QemuHypervisor) GetHypervisorDaemonOverhead() string {
	return "35Mi"
}

// Implement GetHypervisorOverhead method for QemuHypervisor
func (q *QemuHypervisor) GetHypervisorOverhead() string {
	return "30Mi"
}

// Implement GetDefaultKernelPath method for QemuHypervisor
func (q *QemuHypervisor) GetDefaultKernelPath() (string, string) {
	return "", ""
}
