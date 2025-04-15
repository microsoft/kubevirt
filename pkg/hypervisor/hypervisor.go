package hypervisor

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	"kubevirt.io/client-go/log"

	"kubevirt.io/kubevirt/pkg/util"
)

const (
	QemuHypervisorKey  string = "qemu"
	CloudHypervisorKey string = "ch"
)

const (
	vmmConfPathPattern              = "/etc/libvirt/%s.conf"
	vmmModularDaemonConfPathPattern = "/etc/libvirt/%s.conf"
	libvirtRuntimePath              = "/var/run/libvirt"
	libvirtHomePath                 = "/var/run/kubevirt-private/libvirt"
	vmmNonRootConfPathPattern       = libvirtHomePath + "/%s.conf"
)

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

	// Setup libvirt for hosting the virtual machine. This function is called during the startup of the virt-launcher.
	SetupLibvirt(customLogFilters *string) (err error)

	// Start the libvirt daemon, either in modular mode or monolithic mode
	StartHypervisorDaemon(stopChan chan struct{})

	// Start the virtlogd daemon, which is used to capture logs from the hypervisor
	StartVirtlog(stopChan chan struct{}, domainName string)

	GetVmm() string

	Root() bool

	// Return the modular libvirt daemon for the hypervisor
	GetModularDaemonName() string

	// Return the directory where libvirt stores the PID files of the hypervisor processes
	GetPidDir() string

	GetLibvirtUriAndUser() (string, string)

	// Return a list of potential prefixes of the specific hypervisor's process, e.g., qemu-system or cloud-hypervisor
	GetHypervisorCommandPrefix() []string
}

func NewHypervisor(hypervisor string) Hypervisor {
	return NewHypervisorWithUser(hypervisor, false)
}

func NewHypervisorWithUser(hypervisor string, nonRoot bool) Hypervisor {
	if hypervisor == QemuHypervisorKey {
		if nonRoot {
			return &QemuHypervisor{
				user: util.NonRootUID,
			}
		}
		return &QemuHypervisor{
			user: util.RootUser,
		}
	} else if hypervisor == CloudHypervisorKey {
		return &CloudHypervisor{util.RootUser}
	} else {
		return nil
	}
}

func setupLibvirt(l Hypervisor, customLogFilters *string, shouldConfigureVmmConf bool) (err error) {
	if shouldConfigureVmmConf {
		vmmConfPath := fmt.Sprintf(vmmConfPathPattern, l.GetVmm())
		runtimeVmmConfPath := vmmConfPath
		if !l.Root() {
			runtimeVmmConfPath = fmt.Sprintf(vmmNonRootConfPathPattern, l.GetVmm())

			if err := os.MkdirAll(libvirtHomePath, 0755); err != nil {
				return err
			}
			if err := copyFile(vmmConfPath, runtimeVmmConfPath); err != nil {
				return err
			}
		}

		if err := configureVmmConf(runtimeVmmConfPath); err != nil {
			return err
		}
	}

	runtimeVmmDaemonConfPath := path.Join(libvirtRuntimePath, fmt.Sprintf("%s.conf", l.GetModularDaemonName()))
	vmmModularDaemonConfPath := fmt.Sprintf(vmmModularDaemonConfPathPattern, l.GetModularDaemonName())
	if err := copyFile(vmmModularDaemonConfPath, runtimeVmmDaemonConfPath); err != nil {
		return err
	}

	var libvirtLogVerbosityEnvVar *string
	if envVarValue, envVarDefined := os.LookupEnv(util.ENV_VAR_VIRT_LAUNCHER_LOG_VERBOSITY); envVarDefined {
		libvirtLogVerbosityEnvVar = &envVarValue
	}
	_, libvirtDebugLogsEnvVarDefined := os.LookupEnv(util.ENV_VAR_LIBVIRT_DEBUG_LOGS)

	if logFilters, enableDebugLogs := GetLibvirtLogFilters(customLogFilters, libvirtLogVerbosityEnvVar, libvirtDebugLogsEnvVarDefined); enableDebugLogs {
		virtqemudConf, err := os.OpenFile(runtimeVmmDaemonConfPath, os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return err
		}
		defer util.CloseIOAndCheckErr(virtqemudConf, &err)

		log.Log.Infof("Enabling libvirt log filters: %s", logFilters)
		_, err = virtqemudConf.WriteString(fmt.Sprintf("log_filters=\"%s\"\n", logFilters))
		if err != nil {
			return err
		}
	}

	return nil
}

func copyFile(from, to string) error {
	f, err := os.OpenFile(from, os.O_RDONLY, 0644)
	if err != nil {
		return err
	}
	defer util.CloseIOAndCheckErr(f, &err)
	newFile, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer util.CloseIOAndCheckErr(newFile, &err)

	_, err = io.Copy(newFile, f)
	return err
}

func configureVmmConf(vmmConfFilename string) (err error) {
	vmmConf, err := os.OpenFile(vmmConfFilename, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer util.CloseIOAndCheckErr(vmmConf, &err)

	// If hugepages exist, tell libvirt about them
	_, err = os.Stat("/dev/hugepages")
	if err == nil {
		_, err = vmmConf.WriteString("hugetlbfs_mount = \"/dev/hugepages\"\n")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	if envVarValue, ok := os.LookupEnv("VIRTIOFSD_DEBUG_LOGS"); ok && (envVarValue == "1") {
		_, err = vmmConf.WriteString("virtiofsd_debug = 1\n")
		if err != nil {
			return err
		}
	}

	return nil
}

// GetLibvirtLogFilters returns libvirt debug log filters that should be enabled if enableDebugLogs is true.
// The decision is based on the following logic:
//   - If custom log filters are defined - they should be enabled and used.
//   - If verbosity is defined and beyond threshold then debug logs would be enabled and determined by verbosity level
//   - If verbosity level is below threshold but debug logs environment variable is defined, debug logs would be enabled
//     and set to the highest verbosity level.
//   - If verbosity level is below threshold and debug logs environment variable is not defined - debug logs are disabled.
func GetLibvirtLogFilters(customLogFilters, libvirtLogVerbosityEnvVar *string, libvirtDebugLogsEnvVarDefined bool) (logFilters string, enableDebugLogs bool) {

	if customLogFilters != nil && *customLogFilters != "" {
		return *customLogFilters, true
	}

	var libvirtLogVerbosity int
	var err error

	if libvirtLogVerbosityEnvVar != nil {
		libvirtLogVerbosity, err = strconv.Atoi(*libvirtLogVerbosityEnvVar)
		if err != nil {
			log.Log.Infof("cannot apply %s value %s - must be a number", util.ENV_VAR_VIRT_LAUNCHER_LOG_VERBOSITY, *libvirtLogVerbosityEnvVar)
			libvirtLogVerbosity = -1
		}
	} else {
		libvirtLogVerbosity = -1
	}

	const verbosityThreshold = util.EXT_LOG_VERBOSITY_THRESHOLD

	if libvirtLogVerbosity < verbosityThreshold {
		if libvirtDebugLogsEnvVarDefined {
			libvirtLogVerbosity = verbosityThreshold + 5
		} else {
			return "", false
		}
	}

	// Higher log level means higher verbosity
	const logsLevel4 = "3:remote 4:event 3:util.json 3:util.object 3:util.dbus 3:util.netlink 3:node_device 3:rpc 3:access"
	const logsLevel3 = logsLevel4 + " 3:util.threadjob 3:cpu.cpu"
	const logsLevel2 = logsLevel3 + " 3:qemu.qemu_monitor"
	const logsLevel1 = logsLevel2 + " 3:qemu.qemu_monitor_json 3:conf.domain_addr"
	const allowAllOtherCategories = " 1:*"

	switch libvirtLogVerbosity {
	case verbosityThreshold:
		logFilters = logsLevel1
	case verbosityThreshold + 1:
		logFilters = logsLevel2
	case verbosityThreshold + 2:
		logFilters = logsLevel3
	case verbosityThreshold + 3:
		logFilters = logsLevel4
	default:
		logFilters = logsLevel4
	}

	return logFilters + allowAllOtherCategories, true
}

func startModularLibvirtDaemon(l Hypervisor, stopChan chan struct{}) {
	// we spawn libvirt from virt-launcher in order to ensure the virtqemud+qemu process
	// doesn't exit until virt-launcher is ready for it to. Virt-launcher traps signals
	// to perform special shutdown logic. These processes need to live in the same
	// container.

	modularDaemonName := l.GetModularDaemonName()

	go func() {
		for {
			exitChan := make(chan struct{})
			args := []string{"-f", fmt.Sprintf("/var/run/libvirt/%s.conf", modularDaemonName)}
			cmd := exec.Command(fmt.Sprintf("/usr/sbin/%s", modularDaemonName), args...)
			if !l.Root() {
				cmd.SysProcAttr = &syscall.SysProcAttr{
					AmbientCaps: []uintptr{unix.CAP_NET_BIND_SERVICE},
				}
			}

			// connect libvirt's stderr to our own stdout in order to see the logs in the container logs
			reader, err := cmd.StderrPipe()
			if err != nil {
				log.Log.Reason(err).Error(fmt.Sprintf("failed to start %s", modularDaemonName))
				panic(err)
			}

			go func() {
				scanner := bufio.NewScanner(reader)
				scanner.Buffer(make([]byte, 1024), 512*1024)
				for scanner.Scan() {
					log.LogLibvirtLogLine(log.Log, scanner.Text())
				}

				if err := scanner.Err(); err != nil {
					log.Log.Reason(err).Error("failed to read libvirt logs")
				}
			}()

			err = cmd.Start()
			if err != nil {
				log.Log.Reason(err).Error(fmt.Sprintf("failed to start %s", modularDaemonName))
				panic(err)
			}

			go func() {
				defer close(exitChan)
				cmd.Wait()
			}()

			select {
			case <-stopChan:
				cmd.Process.Kill()
				return
			case <-exitChan:
				log.Log.Errorf(fmt.Sprintf("%s exited, restarting", modularDaemonName))
			}

			// this sleep is to avoid consuming all resources in the
			// event of a virtqemud crash loop.
			time.Sleep(time.Second)
		}
	}()
}

func startVirtlogdLogging(virtlogdBinaryPath string, stopChan chan struct{}, domainName string, nonRoot bool) {
	for {
		cmd := exec.Command(virtlogdBinaryPath, "-f", "/etc/libvirt/virtlogd.conf")

		exitChan := make(chan struct{})

		err := cmd.Start()
		if err != nil {
			log.Log.Reason(err).Error("failed to start virtlogd")
			panic(err)
		}

		go func() {
			logfile := fmt.Sprintf("/var/log/libvirt/qemu/%s.log", domainName)
			if nonRoot {
				logfile = filepath.Join("/var", "run", "kubevirt-private", "libvirt", "qemu", "log", fmt.Sprintf("%s.log", domainName))
			}

			// It can take a few seconds to the log file to be created
			for {
				_, err = os.Stat(logfile)
				if !errors.Is(err, os.ErrNotExist) {
					break
				}
				time.Sleep(time.Second)
			}
			// #nosec No risk for path injection. logfile has a static basedir
			file, err := os.Open(logfile)
			if err != nil {
				errMsg := fmt.Sprintf("failed to open logfile in path: \"%s\"", logfile)
				log.Log.Reason(err).Error(errMsg)
				return
			}
			defer util.CloseIOAndCheckErr(file, nil)

			scanner := bufio.NewScanner(file)
			scanner.Buffer(make([]byte, 1024), 512*1024)
			for scanner.Scan() {
				log.LogQemuLogLine(log.Log, scanner.Text())
			}

			if err := scanner.Err(); err != nil {
				log.Log.Reason(err).Error("failed to read virtlogd logs")
			}
		}()

		go func() {
			defer close(exitChan)
			_ = cmd.Wait()
		}()

		select {
		case <-stopChan:
			_ = cmd.Process.Kill()
			return
		case <-exitChan:
			log.Log.Errorf("virtlogd exited, restarting")
		}

		// this sleep is to avoid consuming all resources in the
		// event of a virtlogd crash loop.
		time.Sleep(time.Second)
	}
}
