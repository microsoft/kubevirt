package virt_capabilities

import (
	"encoding/json"
	"os"
)

func ExportVirtualizationCapabilities(v VirtualizationCapabilitiesInterface, filename string) {
	virtCaps := VirtualizationCapabilities{
		SupportedCPUModels:    v.GetSupportedCpuModels(),
		SupportedMachineTypes: v.GetSupportedMachineTypes(),
		HypervFeatures:        v.GetHypervFeatures(),
		HostCpuModelInfo:      v.GetHostCpuModelInfo(),
		SupportedCpuFeatures:  v.GetSupportedCpuFeatures(),
		NodeTscInfo:           v.GetNodeTscInfo(),
		NodeSupportsRealTime:  v.NodeSupportsRealTime(),
		NodeSevFeatures:       v.GetNodeSevFeatures(),
	}

	data, err := json.MarshalIndent(virtCaps, "", "  ")
	if err != nil {
		panic(err)
	}

	file, err := os.Create(filename)
	if err != nil {
		panic(err)
	}
	defer file.Close()

	_, err = file.Write(data)
	if err != nil {
		panic(err)
	}

}
