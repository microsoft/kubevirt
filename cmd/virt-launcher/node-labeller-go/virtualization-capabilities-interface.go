package main

// VirtualizationCapabilitiesInterface defines methods for querying virtualization capabilities.
type VirtualizationCapabilitiesInterface interface {
	// GetHypervFeatures returns a list of features required for Windows guests.
	GetHypervFeatures() ([]string, error)

	// GetNodeTopology returns the node topology.
	GetNodeTopology() (interface{}, error)

	// GetSupportedMachineTypes returns supported machine types.
	GetSupportedMachineTypes() ([]string, error)

	// GetSupportedCpuModels returns supported CPU models.
	GetSupportedCpuModels() ([]string, error)

	// GetHostCpuModelInfo returns host CPU model information.
	GetHostCpuModelInfo() (interface{}, error)

	// GetSupportedCpuFeatures returns supported CPU features.
	GetSupportedCpuFeatures() ([]string, error)

	// GetNodeTscInfo returns node TSC (Time Stamp Counter) information.
	GetNodeTscInfo() (interface{}, error)

	// NodeSupportsRealTime returns true if the node supports real-time capabilities.
	NodeSupportsRealTime() (bool, error)

	// GetNodeSevFeatures returns SEV (Secure Encrypted Virtualization) features of the node.
	GetNodeSevFeatures() ([]string, error)
}
