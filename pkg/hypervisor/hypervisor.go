package hypervisor

import "regexp"

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

	// Return the default kernel path and initrd path for the hypervisor
	// If default kernel is not needed return "", ""
	GetDefaultKernelPath() (string, string)

	// Return the domain type in Libvirt domain XML
	GetDomainType() string
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
