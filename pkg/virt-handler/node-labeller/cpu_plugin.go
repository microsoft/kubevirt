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

package nodelabeller

import (
	"kubevirt.io/kubevirt/pkg/virt-handler/node-labeller/util"
	virt_capabilities "kubevirt.io/kubevirt/pkg/virt-launcher-common/virt-capabilities"
)

const (
	isSupported            string = "yes"
	isUnusable             string = "no"
	isRequired             string = "require"
	NodeLabellerVolumePath        = "/var/lib/kubevirt-node-labeller/"
)

func (n *NodeLabeller) getSupportedCpuModels(obsoleteCPUsx86 map[string]bool) []string {
	supportedCPUModels := make([]string, 0)

	if obsoleteCPUsx86 == nil {
		obsoleteCPUsx86 = util.DefaultObsoleteCPUModels
	}

	for _, model := range n.virtCaps.SupportedCPUModels {
		if _, ok := obsoleteCPUsx86[model]; ok {
			continue
		}
		supportedCPUModels = append(supportedCPUModels, model)
	}

	return supportedCPUModels
}

func (n *NodeLabeller) getSupportedCpuFeatures() cpuFeatures {
	supportedCpuFeatures := make(cpuFeatures)

	for _, feature := range n.virtCaps.SupportedCpuFeatures {
		supportedCpuFeatures[feature] = true
	}

	return supportedCpuFeatures
}

func (n *NodeLabeller) GetHostCpuModel() virt_capabilities.HostCPUModel {
	return n.virtCaps.HostCpuModelInfo
}
