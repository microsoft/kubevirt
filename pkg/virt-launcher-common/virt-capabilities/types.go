package virt_capabilities

type HostCPUModel struct {
	Name             string
	Fallback         string
	RequiredFeatures []string
}

type TscConfig struct {
	HasTscCounter bool
	Frequency     string
	Scalable      string
}

type SEVConfiguration struct {
	SupportedES string
}
