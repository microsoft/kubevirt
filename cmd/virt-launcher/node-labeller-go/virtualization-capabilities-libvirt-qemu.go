package main

import (
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"strings"

	v1 "kubevirt.io/api/core/v1"
	"libvirt.org/go/libvirtxml"

	virt_capabilities "kubevirt.io/kubevirt/pkg/virt-launcher-common/virt-capabilities"
)

const (
	isSupported string = "yes"
	isUnusable  string = "no"
	isRequired  string = "require"
)

// VirtualizationCapabilitiesLibvirtQemu is a dummy implementation of VirtualizationCapabilitiesInterface.
type VirtualizationCapabilitiesLibvirtQemu struct {
	// supportedFeatures.xml path
	SupportedFeaturesPath string
	// domainCapabilities.xml path
	DomainCapabilitiesPath string
	// capabilities.xml path
	CapabilitiesPath string

	HostDomCapabilities   HostDomCapabilities
	SupportedHostFeatures []string
	NodeCapabilities      libvirtxml.Caps

	cpuModelVendor string

	hostCPUModel virt_capabilities.HostCPUModel
}

func NewVirtualizationCapabilitiesLibvirtQemu(supportedFeaturesPath string, domainCapabilitiesPath string, capabilitiesPath string) *VirtualizationCapabilitiesLibvirtQemu {
	cap := VirtualizationCapabilitiesLibvirtQemu{
		SupportedFeaturesPath:  supportedFeaturesPath,
		DomainCapabilitiesPath: domainCapabilitiesPath,
		CapabilitiesPath:       capabilitiesPath}
	cap.loadAll()
	return &cap
}

func (v *VirtualizationCapabilitiesLibvirtQemu) loadAll() {
	v.loadSupportedFeatures()
	v.loadDomainCapabilities()
	v.loadCapabilities()
}

func (v *VirtualizationCapabilitiesLibvirtQemu) loadSupportedFeatures() {
	hostFeatures := SupportedHostFeature{}
	err := v.getStructureFromXMLFile(v.SupportedFeaturesPath, &hostFeatures)
	if err != nil {
		fmt.Printf("Error loading supported features: %v\n", err)
		panic(err)
	}

	usableFeatures := make([]string, 0)
	for _, f := range hostFeatures.Feature {
		if f.Policy == isRequired {
			usableFeatures = append(usableFeatures, f.Name)
		}
	}

	v.SupportedHostFeatures = usableFeatures
}

func (v *VirtualizationCapabilitiesLibvirtQemu) loadDomainCapabilities() {
	hostDomCapabilities := HostDomCapabilities{}
	err := v.getStructureFromXMLFile(v.DomainCapabilitiesPath, &hostDomCapabilities)
	if err != nil {
		fmt.Printf("Error loading domain capabilities: %v\n", err)
		panic(err)
	}

	if hostDomCapabilities.SEV.Supported == "yes" && hostDomCapabilities.SEV.MaxESGuests > 0 {
		hostDomCapabilities.SEV.SupportedES = "yes"
	} else {
		hostDomCapabilities.SEV.SupportedES = "no"
	}

	v.HostDomCapabilities = hostDomCapabilities
}

func (v *VirtualizationCapabilitiesLibvirtQemu) loadCapabilities() {
	capabilities := libvirtxml.Caps{}
	err := v.getStructureFromXMLFile(v.CapabilitiesPath, &capabilities)
	if err != nil {
		panic(fmt.Sprintf("Error loading capabilities: %v\n", err))
	}
	v.NodeCapabilities = capabilities
}

// GetHypervFeatures returns a dummy list of Hyper-V features.
func (v *VirtualizationCapabilitiesLibvirtQemu) GetHypervFeatures() []string {
	// TODO Query actual Hyper-V features from /dev/kvm
	return []string{"hv_relaxed", "hv_vapic"}
}

// GetNodeTopology returns a dummy node topology.
func (v *VirtualizationCapabilitiesLibvirtQemu) GetNodeTopology() interface{} {
	// TODO Get actual node topology
	return map[string]interface{}{"sockets": 1, "cores": 2, "threads": 2}
}

// GetSupportedMachineTypes returns a dummy list of supported machine types.
func (v *VirtualizationCapabilitiesLibvirtQemu) GetSupportedMachineTypes() []string {
	var supportedMachines []string
	for _, guest := range v.NodeCapabilities.Guests {
		fmt.Println("Guest architecture: ", guest.Arch.Name)
		for _, machine := range guest.Arch.Machines {
			supportedMachines = append(supportedMachines, machine.Name)
			fmt.Println("Adding machine type: ", machine.Name)
		}
	}
	return supportedMachines
}

// GetSupportedCpuModels returns a dummy list of supported CPU models.
func (v *VirtualizationCapabilitiesLibvirtQemu) GetSupportedCpuModels() []string {
	// TODO Incorporate obsolete CPU models logic.
	// TODO This can also be done in the virt-handler itself.

	usableModels := make([]string, 0)
	for _, mode := range v.HostDomCapabilities.CPU.Mode {
		if mode.Name == v1.CPUModeHostModel {
			if !true { // TODO This needs to be factored on!n.arch.supportsHostModel() {
				fmt.Printf("host-model cpu mode is not supported for %s architecture", "TODO")
				continue
			}

			v.cpuModelVendor = mode.Vendor.Name
			if v.cpuModelVendor == "" {
				v.cpuModelVendor = "Intel" // TODO n.arch.defaultVendor()
			}

			if len(mode.Model) < 1 {
				panic("host model mode is expected to contain a model")
			}
			if len(mode.Model) > 1 {
				panic("host model mode is expected to contain only one model")
			}

			hostCpuModel := mode.Model[0]
			v.hostCPUModel.Name = hostCpuModel.Name
			v.hostCPUModel.Fallback = hostCpuModel.Fallback
			v.hostCPUModel.Vendor = v.cpuModelVendor

			for _, feature := range mode.Feature {
				if feature.Policy == isRequired {
					v.hostCPUModel.RequiredFeatures = append(v.hostCPUModel.RequiredFeatures, feature.Name)
				}
			}

			fmt.Println("Host CPU Model : ", v.hostCPUModel)

		}

		for _, model := range mode.Model {
			if model.Usable == isUnusable || model.Usable == "" {
				continue
			}
			usableModels = append(usableModels, model.Name)
		}
	}

	return usableModels
}

// GetHostCpuModelInfo returns dummy host CPU model information.
func (v *VirtualizationCapabilitiesLibvirtQemu) GetHostCpuModelInfo() virt_capabilities.HostCPUModel {
	return v.hostCPUModel
}

// GetSupportedCpuFeatures returns a dummy list of supported CPU features.
func (v *VirtualizationCapabilitiesLibvirtQemu) GetSupportedCpuFeatures() []string {
	// TODO The below condition was in the virt-handlr code. Implementation should check which architecture this is and based on that expose SupportedHostFeatures.
	// host supported features is only available on AMD64 and S390X nodes.
	// This is because hypervisor-cpu-baseline virsh command doesnt work for ARM64 architecture.
	// if n.arch.hasHostSupportedFeatures() {
	return v.SupportedHostFeatures
}

// GetNodeTscInfo returns dummy node TSC information.
func (v *VirtualizationCapabilitiesLibvirtQemu) GetNodeTscInfo() virt_capabilities.TscConfig {
	counter := v.NodeCapabilities.Host.CPU.Counter
	if counter != nil && counter.Name == "tsc" {
		return virt_capabilities.TscConfig{
			HasTscCounter: true,
			Frequency:     fmt.Sprintf("%d", counter.Frequency),
			Scalable:      fmt.Sprintf("%t", counter.Scaling == "yes"),
		}
	}
	return virt_capabilities.TscConfig{
		HasTscCounter: false}
}

// NodeSupportsRealTime returns a dummy value indicating real-time support.
func (v *VirtualizationCapabilitiesLibvirtQemu) NodeSupportsRealTime() bool {
	isNodeRealtimeCapable, _ := isNodeRealtimeCapable()
	return isNodeRealtimeCapable
}

// GetNodeSevFeatures returns a dummy list of SEV features.
func (v *VirtualizationCapabilitiesLibvirtQemu) GetNodeSevFeatures() virt_capabilities.SEVConfiguration {
	sevCfg := virt_capabilities.SEVConfiguration{
		Supported:   v.HostDomCapabilities.SEV.Supported,
		SupportedES: v.HostDomCapabilities.SEV.SupportedES,
	}
	return sevCfg
}

// GetStructureFromXMLFile load data from xml file and unmarshals them into given structure
// Given structure has to be pointer
func (v *VirtualizationCapabilitiesLibvirtQemu) getStructureFromXMLFile(path string, structure interface{}) error {
	rawFile, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	//fmt.Printf("node-labeller - loading data from xml file: %#v\n", string(rawFile))

	return xml.Unmarshal(rawFile, structure)
}

// isNodeRealtimeCapable Checks if a node is capable of running realtime workloads. Currently by validating if the kernel system setting value
// for `kernel.sched_rt_runtime_us` is set to allow running realtime scheduling with unlimited time (==-1)
// TODO: This part should be improved to validate against key attributes that determine best if a host is able to run realtime
// workloads at peak performance.

func isNodeRealtimeCapable() (bool, error) {
	ret, err := exec.Command("sysctl", virt_capabilities.KernelSchedRealtimeRuntimeInMicroseconds).CombinedOutput()
	if err != nil {
		return false, err
	}
	st := strings.Trim(string(ret), "\n")
	return fmt.Sprintf("%s = -1", virt_capabilities.KernelSchedRealtimeRuntimeInMicroseconds) == st, nil
}
