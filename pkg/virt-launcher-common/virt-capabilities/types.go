package virt_capabilities

type HostCPUModel struct {
	Name             string   `json:"name"`
	Fallback         string   `json:"fallback"`
	RequiredFeatures []string `json:"requiredFeatures"`
}

type TscConfig struct {
	HasTscCounter bool   `json:"hasTscCounter"`
	Frequency     string `json:"frequency"`
	Scalable      string `json:"scalable"`
}

type SEVConfiguration struct {
	SupportedES string
}

type VirtualizationCapabilities struct {
	SupportedCPUModels    []string         `json:"supportedCpuModels"`
	SupportedMachineTypes []string         `json:"supportedMachineTypes"`
	HypervFeatures        []string         `json:"hypervFeatures"`
	HostCpuModelInfo      HostCPUModel     `json:"hostCpuModelInfo"`
	SupportedCpuFeatures  []string         `json:"supportedCpuFeatures"`
	NodeTscInfo           TscConfig        `json:"nodeTscInfo"`
	NodeSupportsRealTime  bool             `json:"nodeSupportsRealTime"`
	NodeSevFeatures       SEVConfiguration `json:"nodeSevFeatures"`
}
