package hypervisor

import "regexp"

var HypervisorDaemonExecutables []string = []string{"virtqemud", "libvirtd"}
var QemuProcessExecutablePrefixes []string = []string{"qemu-system", "qemu-kvm", "cloud-hypervisor"}

// Hypervisor interface defines functions needed to tune the virt-launcher pod spec and the libvirt domain XML for a specific hypervisor
type Hypervisor interface {
	// The `ps` RSS for virt-launcher-monitor
	GetVirtLauncherMonitorOverhead() string

	// The `ps` RSS for the virt-launcher process
	GetVirtLauncherOverhead() string

	// The `ps` RSS for virtlogd
	GetVirtlogdOverhead() string

	// The `ps` RSS for hypervisor daemon, e.g., virtqemud or libvirtd
	GetHypervisorDaemonOverhead() string

	// The `ps` RSS for vmm, minus the RAM of its (stressed) guest, minus the virtual page table
	GetHypervisorOverhead() string

	// Return true if the hypervisor supports ISO files
	SupportsIso() bool

	// TODO Probably not needed
	SupportsNonRootUser() bool

	// Return the K8s device name that should be exposed for the hypervisor,
	// e.g., devices.kubevirt.io/kvm for QEMU and devices.kubevirt.io/mshv for Cloud Hypervisor
	GetHypervisorDevice() string

	// Return true if the virt-launcher container should run privileged
	ShouldRunPrivileged() bool

	// Return a regex that matches the thread comm value for vCPUs
	GetVcpuRegex() *regexp.Regexp

	// Return the path to the libvirt connection socket file on the virt-launcher pod
	GetLibvirtSocketPath() string

	// Get the disk driver to be used for the hypervisor
	GetDiskDriver() string

	// Return true if the hypervisor requires boot order
	RequiresBootOrder() bool

	// Return true if the hypervisor supports memory ballooning
	SupportsMemoryBallooning() bool
}

// Define QemuHypervisor struct that implements the Hypervisor interface
type QemuHypervisor struct {
}

type CloudHypervisor struct {
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

func (q *QemuHypervisor) SupportsIso() bool {
	return true
}

func (q *QemuHypervisor) SupportsNonRootUser() bool {
	return true
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

func (c *CloudHypervisor) SupportsIso() bool {
	return false
}

func (c *CloudHypervisor) SupportsNonRootUser() bool {
	return false
}

func NewHypervisor(hypervisor string) Hypervisor {
	if hypervisor == "qemu" {
		return &QemuHypervisor{}
	} else if hypervisor == "ch" {
		return &CloudHypervisor{}
	} else {
		return nil
	}
}
