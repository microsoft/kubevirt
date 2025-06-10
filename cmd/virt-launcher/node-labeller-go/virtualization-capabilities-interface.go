package main

const kernelSchedRealtimeRuntimeInMicrosecods = "kernel.sched_rt_runtime_us"

const (
	HypervFeaturesKey        = "hyperv-features"
	SupportedMachineTypeKeys = "supported-machine-types"
	SupportedCpuModelsKey    = "supported-cpu-models"
	HostCpuModelInfoKey      = "host-cpu-model-info"
	SupportedCpuFeaturesKey  = "supported-cpu-features"
	NodeTscInfoKey           = "node-tsc-info"
	NodeSupportsRealTimeKey  = "node-supports-real-time"
	NodeSevFeaturesKey       = "node-sev-features"
)

// VirtualizationCapabilitiesInterface defines methods for querying virtualization capabilities.
type VirtualizationCapabilitiesInterface interface {
	// GetHypervFeatures returns a list of features required for Windows guests.
	GetHypervFeatures() ([]string, error)

	// GetSupportedMachineTypes returns supported machine types.
	GetSupportedMachineTypes() ([]string, error)

	// GetSupportedCpuModels returns supported CPU models.
	GetSupportedCpuModels() ([]string, error)

	// GetHostCpuModelInfo returns host CPU model information.
	GetHostCpuModelInfo() (hostCPUModel, error)

	// GetSupportedCpuFeatures returns supported CPU features.
	GetSupportedCpuFeatures() ([]string, error)

	// GetNodeTscInfo returns node TSC (Time Stamp Counter) information.
	GetNodeTscInfo() (TscConfig, error)

	// NodeSupportsRealTime returns true if the node supports real-time capabilities.
	NodeSupportsRealTime() (bool, error)

	// GetNodeSevFeatures returns SEV (Secure Encrypted Virtualization) features of the node.
	GetNodeSevFeatures() (SEVConfiguration, error)
}
