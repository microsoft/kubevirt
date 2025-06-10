package virt_capabilities

import (
	"encoding/json"
	"os"
)

func ExportVirtualizationCapabilities(v VirtualizationCapabilitiesInterface, filename string) {
	virtCaps := make(map[string]interface{})
	virtCaps[HypervFeaturesKey], _ = v.GetHypervFeatures()
	virtCaps[SupportedMachineTypeKeys], _ = v.GetSupportedMachineTypes()
	virtCaps[SupportedCpuModelsKey], _ = v.GetSupportedCpuModels()
	virtCaps[HostCpuModelInfoKey], _ = v.GetHostCpuModelInfo()
	virtCaps[SupportedCpuFeaturesKey], _ = v.GetSupportedCpuFeatures()
	virtCaps[NodeTscInfoKey], _ = v.GetNodeTscInfo()
	virtCaps[NodeSupportsRealTimeKey], _ = v.NodeSupportsRealTime()
	virtCaps[NodeSevFeaturesKey], _ = v.GetNodeSevFeatures()

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
