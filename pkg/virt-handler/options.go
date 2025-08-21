/*
Copyright 2024 The KubeVirt Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package virthandler

import (
	v1 "kubevirt.io/api/core/v1"

	virtconfig "kubevirt.io/kubevirt/pkg/virt-config"

	cmdv1 "kubevirt.io/kubevirt/pkg/handler-launcher-com/cmd/v1"
)

// TODO: This file needs to be updated.
// TODO: virtualMachineOptions function args should have topology instead of capabilities
// TODO: Remove topology conversion functions below

func virtualMachineOptions(
	smbios *v1.SMBiosConfiguration,
	period uint32,
	preallocatedVolumes []string,
	nodeTopology *cmdv1.Topology,
	clusterConfig *virtconfig.ClusterConfig,
) *cmdv1.VirtualMachineOptions {
	options := &cmdv1.VirtualMachineOptions{
		MemBalloonStatsPeriod: period,
		PreallocatedVolumes:   preallocatedVolumes,
		Topology:              nodeTopology,
		// New virt-launcher images no longer use this value, it's kept empty for backward compatibility.
		DisksInfo: map[string]*cmdv1.DiskInfo{},
	}
	if smbios != nil {
		options.VirtualMachineSMBios = &cmdv1.SMBios{
			Family:       smbios.Family,
			Product:      smbios.Product,
			Manufacturer: smbios.Manufacturer,
			Sku:          smbios.Sku,
			Version:      smbios.Version,
		}
	}

	if clusterConfig != nil {
		bochsDisplay := true
		if clusterConfig.VGADisplayForEFIGuestsEnabled() {
			bochsDisplay = false
		}
		options.ExpandDisksEnabled = clusterConfig.ExpandDisksEnabled()
		options.ClusterConfig = &cmdv1.ClusterConfig{
			ExpandDisksEnabled:        clusterConfig.ExpandDisksEnabled(),
			FreePageReportingDisabled: clusterConfig.IsFreePageReportingDisabled(),
			BochsDisplayForEFIGuests:  bochsDisplay,
			SerialConsoleLogDisabled:  clusterConfig.IsSerialConsoleLogDisabled(),
		}
	}

	return options
}
