package virt_launcher_common

//go:generate mockgen -source $GOFILE -package=$GOPACKAGE -destination=generated_mock_$GOFILE

/*
 ATTENTION: Rerun code generators when interface signatures are modified.
*/

import (
	v1 "kubevirt.io/api/core/v1"

	cmdv1 "kubevirt.io/kubevirt/pkg/handler-launcher-com/cmd/v1"
	cmdclient "kubevirt.io/kubevirt/pkg/virt-handler/cmd-client"
	"kubevirt.io/kubevirt/pkg/virt-launcher-common/api"
	"kubevirt.io/kubevirt/pkg/virt-launcher-common/stats"
)

type DomainManager interface {
	SyncVMI(*v1.VirtualMachineInstance, bool, *cmdv1.VirtualMachineOptions) (*api.DomainSpec, error)
	PauseVMI(*v1.VirtualMachineInstance) error
	UnpauseVMI(*v1.VirtualMachineInstance) error
	FreezeVMI(*v1.VirtualMachineInstance, int32) error
	UnfreezeVMI(*v1.VirtualMachineInstance) error
	ResetVMI(*v1.VirtualMachineInstance) error
	SoftRebootVMI(*v1.VirtualMachineInstance) error
	KillVMI(*v1.VirtualMachineInstance) error
	DeleteVMI(*v1.VirtualMachineInstance) error
	SignalShutdownVMI(*v1.VirtualMachineInstance) error
	MarkGracefulShutdownVMI()
	ListAllDomains() ([]*api.Domain, error)
	MigrateVMI(*v1.VirtualMachineInstance, *cmdclient.MigrationOptions) error
	PrepareMigrationTarget(*v1.VirtualMachineInstance, bool, *cmdv1.VirtualMachineOptions) error
	GetDomainStats() (*stats.DomainStats, error)
	CancelVMIMigration(*v1.VirtualMachineInstance) error
	GetGuestInfo() v1.VirtualMachineInstanceGuestAgentInfo
	GetUsers() []v1.VirtualMachineInstanceGuestOSUser
	GetFilesystems() []v1.VirtualMachineInstanceFileSystem
	FinalizeVirtualMachineMigration(*v1.VirtualMachineInstance, *cmdv1.VirtualMachineOptions) error
	HotplugHostDevices(vmi *v1.VirtualMachineInstance) error
	InterfacesStatus() []api.InterfaceStatus
	GetGuestOSInfo() *api.GuestOSInfo
	Exec(string, string, []string, int32) (string, error)
	GuestPing(string) error
	MemoryDump(vmi *v1.VirtualMachineInstance, dumpPath string) error
	GetQemuVersion() (string, error)
	UpdateVCPUs(vmi *v1.VirtualMachineInstance, options *cmdv1.VirtualMachineOptions) error
	GetSEVInfo() (*v1.SEVPlatformInfo, error)
	GetLaunchMeasurement(*v1.VirtualMachineInstance) (*v1.SEVMeasurementInfo, error)
	InjectLaunchSecret(*v1.VirtualMachineInstance, *v1.SEVSecretOptions) error
	UpdateGuestMemory(vmi *v1.VirtualMachineInstance) error
	FormatError(err error) string
}
