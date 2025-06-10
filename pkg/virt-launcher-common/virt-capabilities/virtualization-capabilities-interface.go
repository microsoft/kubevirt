package virt_capabilities

const KernelSchedRealtimeRuntimeInMicroseconds = "kernel.sched_rt_runtime_us"

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
	GetHypervFeatures() []string

	// GetSupportedMachineTypes returns supported machine types.
	GetSupportedMachineTypes() []string

	// GetSupportedCpuModels returns supported CPU models.
	GetSupportedCpuModels() []string

	// GetHostCpuModelInfo returns host CPU model information.
	GetHostCpuModelInfo() HostCPUModel

	// GetSupportedCpuFeatures returns supported CPU features.
	GetSupportedCpuFeatures() []string

	// GetNodeTscInfo returns node TSC (Time Stamp Counter) information.
	GetNodeTscInfo() TscConfig

	// NodeSupportsRealTime returns true if the node supports real-time capabilities.
	NodeSupportsRealTime() bool

	// GetNodeSevFeatures returns SEV (Secure Encrypted Virtualization) features of the node.
	GetNodeSevFeatures() SEVConfiguration
}
