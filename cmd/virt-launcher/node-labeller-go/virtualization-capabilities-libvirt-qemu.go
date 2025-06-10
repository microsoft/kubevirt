package main

import (
	"encoding/xml"
	"fmt"
	"os"

	v1 "kubevirt.io/api/core/v1"
	"libvirt.org/go/libvirtxml"
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

	hostCPUModel hostCPUModel
}

func NewVirtualizationCapabilitiesLibvirtQemu(supportedFeaturesPath string, domainCapabilitiesPath string, capabilitiesPath string) *VirtualizationCapabilitiesLibvirtQemu {
	cap := VirtualizationCapabilitiesLibvirtQemu{supportedFeaturesPath, domainCapabilitiesPath, capabilitiesPath, HostDomCapabilities{}, []string{}, libvirtxml.Caps{}, "", hostCPUModel{requiredFeatures: make(map[string]bool)}}
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
		if f.Policy == RequirePolicy {
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
func (v *VirtualizationCapabilitiesLibvirtQemu) GetHypervFeatures() ([]string, error) {
	// TODO Query actual Hyper-V features from /dev/kvm
	return []string{"hv_relaxed", "hv_vapic"}, nil
}

// GetNodeTopology returns a dummy node topology.
func (v *VirtualizationCapabilitiesLibvirtQemu) GetNodeTopology() (interface{}, error) {
	// TODO Get actual node topology
	return map[string]interface{}{"sockets": 1, "cores": 2, "threads": 2}, nil
}

// GetSupportedMachineTypes returns a dummy list of supported machine types.
func (v *VirtualizationCapabilitiesLibvirtQemu) GetSupportedMachineTypes() ([]string, error) {
	var supportedMachines []string
	for _, guest := range v.NodeCapabilities.Guests {
		fmt.Println("Guest architecture: ", guest.Arch.Name)
		for _, machine := range guest.Arch.Machines {
			supportedMachines = append(supportedMachines, machine.Name)
			fmt.Println("Adding machine type: ", machine.Name)
		}
	}
	return supportedMachines, nil
}

// GetSupportedCpuModels returns a dummy list of supported CPU models.
func (v *VirtualizationCapabilitiesLibvirtQemu) GetSupportedCpuModels() ([]string, error) {
	// TODO Incorporate obsolete CPU models logic

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
			v.hostCPUModel.fallback = hostCpuModel.Fallback

			for _, feature := range mode.Feature {
				if feature.Policy == isRequired {
					v.hostCPUModel.requiredFeatures[feature.Name] = true
				}
			}
		}

		for _, model := range mode.Model {
			if model.Usable == isUnusable || model.Usable == "" {
				continue
			}
			usableModels = append(usableModels, model.Name)
		}
	}

	return usableModels, nil
}

// GetHostCpuModelInfo returns dummy host CPU model information.
func (v *VirtualizationCapabilitiesLibvirtQemu) GetHostCpuModelInfo() (interface{}, error) {
	return map[string]interface{}{"model": "Intel", "vendor": "GenuineIntel"}, nil
}

// GetSupportedCpuFeatures returns a dummy list of supported CPU features.
func (v *VirtualizationCapabilitiesLibvirtQemu) GetSupportedCpuFeatures() ([]string, error) {
	return []string{"vmx", "aes", "fma"}, nil
}

// GetNodeTscInfo returns dummy node TSC information.
func (v *VirtualizationCapabilitiesLibvirtQemu) GetNodeTscInfo() (interface{}, error) {
	return map[string]interface{}{"frequency": 2500000000}, nil
}

// NodeSupportsRealTime returns a dummy value indicating real-time support.
func (v *VirtualizationCapabilitiesLibvirtQemu) NodeSupportsRealTime() (bool, error) {
	return true, nil
}

// GetNodeSevFeatures returns a dummy list of SEV features.
func (v *VirtualizationCapabilitiesLibvirtQemu) GetNodeSevFeatures() ([]string, error) {
	return []string{"sev", "sev-es"}, nil
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
