package hypervisor

import "regexp"

// TODO These global variables should be changed to accessor functions in the Hypervisor interface
var HypervisorDaemonExecutables []string = []string{"virtqemud", "virtchd"}

type CloudHypervisor struct {
}

// Implement SupportsMemoryBallooning method for CloudHypervisor
func (c *CloudHypervisor) SupportsMemoryBallooning() bool {
	return false
}

// Implement RequiresBoot order method for CloudHypervisor
func (c *CloudHypervisor) RequiresBootOrder() bool {
	return true
}

// Implement GetDiskDriver method for CloudHypervisor
func (c *CloudHypervisor) GetDiskDriver() string {
	return "raw"
}

// Implement GetLibvirtSocketPath method for CloudHypervisor
func (c *CloudHypervisor) GetLibvirtSocketPath() string {
	return "libvirt/ch-sock" // TODO: Check this
}

// Implement GetVcpuRegex method for CloudHypervisor
func (c *CloudHypervisor) GetVcpuRegex() *regexp.Regexp {
	// parse thread comm value expression for MSHV
	return regexp.MustCompile(`^vcpu(\d+)\n$`) // These threads follow this naming pattern as their command value (/proc/{pid}/task/{taskid}/comm)
}

// Implement ShouldRunPrivileged method for CloudHypervisor
func (c *CloudHypervisor) ShouldRunPrivileged() bool {
	return true
}

// Implement GetHypervisorDevice method for CloudHypervisor
func (c *CloudHypervisor) GetHypervisorDevice() string {
	return "devices.kubevirt.io/mshv"
}

// Implement GetVirtLauncherMonitorOverhead method for CloudHypervisor
func (c *CloudHypervisor) GetVirtLauncherMonitorOverhead() string {
	return "25Mi"
}

// Implement GetVirtLauncherOverhead method for CloudHypervisor
func (c *CloudHypervisor) GetVirtLauncherOverhead() string {
	return "100Mi"
}

// Implement GetVirtlogdOverhead method for CloudHypervisor
func (c *CloudHypervisor) GetVirtlogdOverhead() string {
	return "20Mi"
}

// Implement GetHypervisorDaemonOverhead method for CloudHypervisor
func (c *CloudHypervisor) GetHypervisorDaemonOverhead() string {
	return "35Mi"
}

// Implement GetHypervisorOverhead method for CloudHypervisor
func (c *CloudHypervisor) GetHypervisorOverhead() string {
	return "30Mi"
}

// Implement GetDefaultKernelPath method for CloudHypervisor
func (c *CloudHypervisor) GetDefaultKernelPath() (string, string) {
	return "/usr/share/cloud-hypervisor/CLOUDHV_EFI.fd", ""
}
