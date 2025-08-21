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
 * Copyright the KubeVirt Authors.
 *
 */

package nodelabeller

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	cmdv1 "kubevirt.io/kubevirt/pkg/handler-launcher-com/cmd/v1"
)

const (
	sysfsNodePath = "/sys/devices/system/node/"
	kilobyte      = 1024
)

func readMemTotalKB(meminfoPath string) uint64 {
	data, err := ioutil.ReadFile(meminfoPath)
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "Node") && strings.Contains(line, "MemTotal") {
			fields := strings.Fields(line)
			if len(fields) >= 4 {
				val, err := strconv.ParseUint(fields[3], 10, 64)
				if err == nil {
					return val
				}
			}
		}
	}
	return 0
}

func getHugepageSizes(hugepagesDir string) []uint64 {
	hugepageSizes := make([]uint64, 0)
	entries, err := os.ReadDir(hugepagesDir)
	if err != nil {
		return hugepageSizes
	}

	// Iterate over the entries in the hugepages directory
	for _, entry := range entries {
		name := entry.Name()
		parts := strings.Split(name, "-")
		if len(parts) < 2 {
			continue
		}
		sizeStr := strings.TrimSuffix(parts[1], "kB")
		pageSizeKB, err := strconv.ParseUint(sizeStr, 10, 64)
		if err != nil {
			continue
		}

		hugepageSizes = append(hugepageSizes, pageSizeKB)
	}

	return hugepageSizes
}

func getAvailableHugepages(hugepagesDir string, size uint64) (uint64, error) {
	nrPath := filepath.Join(hugepagesDir, fmt.Sprintf("hugepages-%dkB/nr_hugepages", size))
	nrData, err := ioutil.ReadFile(nrPath)
	if err != nil {
		return 0, err
	}
	nrPages, err := strconv.ParseUint(strings.TrimSpace(string(nrData)), 10, 64)
	if err != nil {
		return 0, err
	}
	return nrPages, nil
}

func populatePageInfo(cell *cmdv1.Cell, node string, systemPageSize int) error {
	meminfoPath := filepath.Join(node, "meminfo")
	totalMemKB := readMemTotalKB(meminfoPath)

	hugepagesDir := filepath.Join(node, "hugepages")
	hugepageSizes := getHugepageSizes(hugepagesDir)

	var hugepagesMemKB uint64 = 0

	for _, sizeKB := range hugepageSizes {
		// Read the number of available hugepages for this size
		nrHugepages, err := getAvailableHugepages(hugepagesDir, sizeKB)
		if err != nil {
			return err
		}

		cell.Pages = append(cell.Pages, &cmdv1.Pages{
			Count: nrHugepages,
			Unit:  "KiB",
			Size:  uint32(sizeKB),
		})

		hugepagesMemKB += sizeKB * nrHugepages
	}

	// The remaining memory is divided into pages of the regular systemPageSize
	regularMemKB := totalMemKB - hugepagesMemKB
	regularMemBytes := regularMemKB * kilobyte

	cell.Pages = append(cell.Pages, &cmdv1.Pages{
		Count: regularMemBytes / uint64(systemPageSize),
		Unit:  "KiB",
		Size:  uint32(systemPageSize / kilobyte),
	})

	return nil
}

func populateMemoryInfo(cell *cmdv1.Cell, node string) error {
	memInfoPath := filepath.Join(node, "meminfo")
	memInfoBytes, _ := ioutil.ReadFile(memInfoPath)
	for _, line := range strings.Split(string(memInfoBytes), "\n") {
		if strings.Contains(line, "MemTotal") {
			// Extract the total memory in kB
			fields := strings.Fields(line)
			if len(fields) >= 4 {
				totalMemKB, err := strconv.ParseUint(fields[3], 10, 64)
				if err == nil {
					cell.Memory = &cmdv1.Memory{
						Unit:   "KiB",
						Amount: totalMemKB,
					}
					return nil
				} else {
					return err
				}
			}
		}
	}
	return fmt.Errorf("failed to parse memory info for node %s", node)
}

func populateDistanceInfo(cell *cmdv1.Cell, node string) error {
	distancePath := filepath.Join(node, "distance")
	distanceBytes, err := ioutil.ReadFile(distancePath)
	if err != nil {
		return err
	}
	distances := strings.Fields(string(distanceBytes))
	for siblingId, distanceStr := range distances {
		distance, err := strconv.ParseUint(distanceStr, 10, 64)
		if err != nil {
			return err
		}
		cell.Distances = append(cell.Distances, &cmdv1.Sibling{
			Id:    uint32(siblingId),
			Value: distance})
	}
	return nil
}

func populateCpus(cell *cmdv1.Cell, node string) error {
	cpuDirs, err := filepath.Glob(filepath.Join(node, "cpu[0-9]*"))
	if err != nil {
		return err
	}

	for _, cpuDir := range cpuDirs {
		cpuName := filepath.Base(cpuDir)
		cpuIDStr := strings.TrimPrefix(cpuName, "cpu")
		cpuID, err := strconv.ParseUint(cpuIDStr, 10, 64)
		if err != nil {
			continue
		}

		// Read thread siblings
		siblingsPath := filepath.Join(cpuDir, "topology/thread_siblings_list")
		siblingsBytes, err := ioutil.ReadFile(siblingsPath)
		if err != nil {
			continue
		}
		siblingsStr := strings.TrimSpace(string(siblingsBytes))
		siblings := parseCPURange(siblingsStr)

		cell.Cpus = append(cell.Cpus, &cmdv1.CPU{
			Id:       uint32(cpuID),
			Siblings: siblings,
		})
	}
	return nil
}

func parseCPURange(cpuRange string) []uint32 {
	var cpus []uint32
	parts := strings.Split(cpuRange, ",")
	for _, part := range parts {
		if strings.Contains(part, "-") {
			bounds := strings.Split(part, "-")
			start, _ := strconv.Atoi(bounds[0])
			end, _ := strconv.Atoi(bounds[1])
			for i := start; i <= end; i++ {
				cpus = append(cpus, uint32(i))
			}
		} else {
			val, _ := strconv.Atoi(part)
			cpus = append(cpus, uint32(val))
		}
	}
	return cpus
}

func ReadNodeTopology() (*cmdv1.Topology, error) {
	topology := &cmdv1.Topology{}

	systemPageSize := unix.Getpagesize()
	if systemPageSize <= 0 {
		return nil, fmt.Errorf("failed to get system page size. It must be greater than 0")
	}

	// Iterate over the different NUMA nodes
	nodes, _ := filepath.Glob(filepath.Join(sysfsNodePath, "node[0-9]*"))
	for _, node := range nodes {
		cellId, err := strconv.ParseUint(strings.TrimPrefix(filepath.Base(node), "node"), 10, 32)
		if err != nil {
			continue
		}

		cell := &cmdv1.Cell{
			Id: uint32(cellId),
		}

		err = populateMemoryInfo(cell, node)
		if err != nil {
			return nil, err
		}

		err = populatePageInfo(cell, node, systemPageSize)
		if err != nil {
			return nil, err
		}

		err = populateDistanceInfo(cell, node)
		if err != nil {
			return nil, err
		}

		err = populateCpus(cell, node)
		if err != nil {
			return nil, err
		}

		topology.NumaCells = append(topology.NumaCells, cell)
	}

	return topology, nil
}
