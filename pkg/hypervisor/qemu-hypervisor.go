package hypervisor

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"syscall"

	"kubevirt.io/client-go/log"

	"kubevirt.io/kubevirt/pkg/util"
)

var QemuProcessExecutablePrefixes []string = []string{"qemu-system", "qemu-kvm", "cloud-hypervisor"}

const QEMUSeaBiosDebugPipe = "/var/run/kubevirt-private/QEMUSeaBiosDebugPipe"

// Define QemuHypervisor struct that implements the Hypervisor interface
type QemuHypervisor struct {
	vmm  string
	user uint32
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

// Implement GetDomainType method for QemuHypervisor
func (c *QemuHypervisor) GetDomainType() string {
	return "kvm"
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

func (q *QemuHypervisor) SetupLibvirt(customLogFilters *string) (err error) {
	return setupLibvirt(q, customLogFilters, true)
}

func (q *QemuHypervisor) GetVmm() string {
	return "qemu"
}

func (q *QemuHypervisor) root() bool {
	return q.user == util.RootUser
}

func (q *QemuHypervisor) GetModularDaemonName() string {
	return "virtqemud"
}

func (q *QemuHypervisor) StartHypervisorDaemon(stopChan chan struct{}) {
	startModularLibvirtDaemon(q, stopChan)
}

func (q *QemuHypervisor) GetPidDir() string {
	if q.root() {
		return "/run/libvirt/qemu"
	} else {
		return "/run/libvirt/qemu/run"
	}
}

func (q *QemuHypervisor) GetLibvirtUriAndUser() (string, string) {
	libvirtUri := "qemu:///system"
	user := ""
	if !q.root() {
		user = util.NonRootUserString
		libvirtUri = "qemu+unix:///session?socket=/var/run/libvirt/virtqemud-sock"
	}
	return libvirtUri, user
}

func (q *QemuHypervisor) GetHypervisorCommandPrefix() []string {
	// give qemu some time to shut down in case it survived virt-handler
	// Most of the time we call `qemu-system=* binaries, but qemu-system-* packages
	// are not everywhere available where libvirt and qemu are. There we usually call qemu-kvm
	// which resides in /usr/libexec/qemu-kvm
	return []string{"qemu-system", "qemu-kvm"}
}

func (l *QemuHypervisor) StartVirtlog(stopChan chan struct{}, domainName string) {
	go startVirtlogdLogging("/usr/sbin/virtlogd", stopChan, domainName, l.user != util.RootUser)
	go startQEMUSeaBiosLogging(stopChan)
}

func startQEMUSeaBiosLogging(stopChan chan struct{}) {
	const QEMUSeaBiosDebugPipeMode uint32 = 0666
	const logLinePrefix = "[SeaBios]:"

	err := syscall.Mkfifo(QEMUSeaBiosDebugPipe, QEMUSeaBiosDebugPipeMode)
	if err != nil {
		log.Log.Reason(err).Error(fmt.Sprintf("%s failed creating a pipe for sea bios debug logs", logLinePrefix))
		return
	}

	// Chmod is needed since umask is 0018. Therefore Mkfifo does not actually create a pipe with proper permissions.
	err = syscall.Chmod(QEMUSeaBiosDebugPipe, QEMUSeaBiosDebugPipeMode)
	if err != nil {
		log.Log.Reason(err).Error(fmt.Sprintf("%s failed executing chmod on pipe for sea bios debug logs.", logLinePrefix))
		return
	}

	QEMUPipe, err := os.OpenFile(QEMUSeaBiosDebugPipe, os.O_RDONLY, 0604)

	if err != nil {
		log.Log.Reason(err).Error(fmt.Sprintf("%s failed to open %s", logLinePrefix, QEMUSeaBiosDebugPipe))
		return
	}
	defer QEMUPipe.Close()

	scanner := bufio.NewScanner(QEMUPipe)
	for {
		for scanner.Scan() {
			logLine := fmt.Sprintf("%s %s", logLinePrefix, scanner.Text())

			log.LogQemuLogLine(log.Log, logLine)

			select {
			case <-stopChan:
				return
			default:
			}
		}

		if err := scanner.Err(); err != nil {
			log.Log.Reason(err).Error(fmt.Sprintf("%s reader failed with an error", logLinePrefix))
			return
		}

		log.Log.Errorf(fmt.Sprintf("%s exited, restarting", logLinePrefix))
	}
}
