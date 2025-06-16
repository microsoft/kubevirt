/*
 * This file is part of the KubeVirt project
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * Copyright The KubeVirt Authors.
 *
 */

package virtwrap

//go:generate mockgen -source $GOFILE -package=$GOPACKAGE -destination=generated_mock_$GOFILE

/*
 ATTENTION: Rerun code generators when interface signatures are modified.
*/

import (
	"time"

	v1 "kubevirt.io/api/core/v1"

	cmdv1 "kubevirt.io/kubevirt/pkg/handler-launcher-com/cmd/v1"
	cmdclient "kubevirt.io/kubevirt/pkg/virt-handler/cmd-client"
	"kubevirt.io/kubevirt/pkg/virt-launcher/virtwrap/api"
	"kubevirt.io/kubevirt/pkg/virt-launcher/virtwrap/stats"
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
	GetDomainDirtyRateStats(calculationDuration time.Duration) (*stats.DomainStatsDirtyRate, error)
	FormatError(err error) string
}
