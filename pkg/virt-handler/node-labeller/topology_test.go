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

package nodelabeller

import (
	"fmt"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	cmdv1 "kubevirt.io/kubevirt/pkg/handler-launcher-com/cmd/v1"

	"os"
	"strings"
)

/*
4. Read distances properly
6. parseCPURange should work for both single CPU, comma-separated ranges and hyphen-separated ranges.

*/

// test function to create and populate a temporary directory
// with node memory and hugepages info for testing topology.go functions.
func writeNodeWithMemoryInfo(dir string, nodeID int, memTotalKB uint64, hugepages map[uint32]uint64) error {
	nodeDir := filepath.Join(dir, fmt.Sprintf("node%d", nodeID))
	err := os.MkdirAll(nodeDir, 0755)
	if err != nil {
		return err
	}

	meminfoPath := filepath.Join(nodeDir, "meminfo")
	meminfoContent := fmt.Sprintf("Node %d MemTotal: %d kB\n", nodeID, memTotalKB)
	err = os.WriteFile(meminfoPath, []byte(meminfoContent), 0644)
	if err != nil {
		return err
	}

	hugepagesDir := filepath.Join(nodeDir, "hugepages")
	err = os.MkdirAll(hugepagesDir, 0755)
	if err != nil {
		return err
	}

	for sizeKB, count := range hugepages {
		sizeStr := fmt.Sprintf("hugepages-%dkB", sizeKB)
		sizeDir := filepath.Join(hugepagesDir, sizeStr)
		err = os.MkdirAll(sizeDir, 0755)
		if err != nil {
			return err
		}
		nrHugepagesPath := filepath.Join(sizeDir, "nr_hugepages")
		err = os.WriteFile(nrHugepagesPath, []byte(fmt.Sprintf("%d\n", count)), 0644)
		if err != nil {
			return err
		}
	}

	return nil
}

// test function to create and populate a temporary directory
// with cpus and thread_siblings for testing topology.go functions.
func writeNodeWithCPUInfo(dir string, nodeID int, cpus []int, threadSiblings map[int][]int) error {
	nodeDir := filepath.Join(dir, fmt.Sprintf("node%d", nodeID))
	err := os.MkdirAll(nodeDir, 0755)
	if err != nil {
		return err
	}

	for _, cpu := range cpus {
		cpuSubDir := filepath.Join(nodeDir, fmt.Sprintf("cpu%d", cpu))
		err = os.MkdirAll(cpuSubDir, 0755)
		if err != nil {
			return err
		}
		siblings, ok := threadSiblings[cpu]
		if !ok {
			siblings = []int{}
		}
		siblingStrs := make([]string, len(siblings))
		for i, sib := range siblings {
			siblingStrs[i] = fmt.Sprintf("%d", sib)
		}
		topologyDir := filepath.Join(cpuSubDir, "topology")
		err := os.MkdirAll(topologyDir, 0755)
		if err != nil {
			return err
		}
		threadSiblingsPath := filepath.Join(cpuSubDir, "topology", "thread_siblings_list")
		err = os.MkdirAll(filepath.Dir(threadSiblingsPath), 0755)
		if err != nil {
			return err
		}
		err = os.WriteFile(threadSiblingsPath, []byte(fmt.Sprintf("%s\n", strings.Join(siblingStrs, ","))), 0644)
		if err != nil {
			return err
		}
	}

	return nil
}

var _ = Describe("Extracting Node Topology", func() {
	var (
		tempDir        string
		systemPageSize uint64 = 4 * 1024 // Assume 4KB system page size for testing
	)

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "topology-test-*")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		os.RemoveAll(tempDir)
	})

	Context("when reading memory info", func() {
		It("should return the correct memory size", func() {
			nodeId := 0
			cell := &cmdv1.Cell{
				Id: uint32(nodeId),
			}
			writeNodeWithMemoryInfo(tempDir, nodeId, 16384*1024, map[uint32]uint64{2048: 0, 1048576: 0})
			nodeDir := filepath.Join(tempDir, fmt.Sprintf("node%d", nodeId))
			populateMemoryInfo(cell, nodeDir)

			Expect(cell.Memory).NotTo(BeNil())
			Expect(cell.Memory.Amount).To(Equal(uint64(16384 * 1024)))
			Expect(cell.Memory.Unit).To(Equal("KiB"))
		})

		DescribeTable("it should read correct page info when", func(hugepagesMap map[uint32]uint64) {
			nodeId := 0
			cell := &cmdv1.Cell{
				Id: uint32(nodeId),
			}
			memTotalKB := uint64(16384 * 1024) // 16GB in KiB
			writeNodeWithMemoryInfo(tempDir, nodeId, memTotalKB, hugepagesMap)
			nodeDir := filepath.Join(tempDir, fmt.Sprintf("node%d", nodeId))
			populatePageInfo(cell, nodeDir, systemPageSize)

			Expect(cell.Pages).NotTo(BeNil())
			// There should be one entry for the system page size plus one for each hugepage size configured
			Expect(len(cell.Pages)).To(Equal(1 + len(hugepagesMap)))
			// Check that each hugepage size configured is present in the cell.Pages
			totalHugepagesMemKB := uint64(0)
			for size, count := range hugepagesMap {
				found := false
				for _, page := range cell.Pages {
					if page.Size == size && page.Count == count {
						found = true
						totalHugepagesMemKB += uint64(size) * count
						break
					}
				}
				Expect(found).To(BeTrue(), fmt.Sprintf("Expected to find hugepage size %d with count %d", size, count))
			}
			// Check that the system page size is present with the right count
			systemPageCount := (memTotalKB - totalHugepagesMemKB) / (systemPageSize / 1024)
			foundSystemPage := false
			for _, page := range cell.Pages {
				if page.Size == uint32(systemPageSize/1024) {
					foundSystemPage = true
					Expect(page.Count).To(Equal(systemPageCount), fmt.Sprintf("Expected system page count to be %d", systemPageCount))
					break
				}
			}
			Expect(foundSystemPage).To(BeTrue(), fmt.Sprintf("Expected to find system page size %d", systemPageSize))
		},
			Entry("no hugepages are configured", map[uint32]uint64{}),
			Entry("zero hugepages are configured", map[uint32]uint64{2048: 0, 1048576: 0}),
			Entry("only size of hugepages are configured", map[uint32]uint64{2048: 0, 1048576: 4}),
			Entry("multiple sizes of hugepages are configured", map[uint32]uint64{2048: 16, 1048576: 4}),
		)
	})

	Context("when reading CPU info", func() {
		DescribeTable("it should read correct CPU Ids and thread siblings when", func(cpus []int, threadSiblings map[int][]int) {
			nodeId := 0
			cell := &cmdv1.Cell{
				Id: uint32(nodeId),
			}
			writeNodeWithCPUInfo(tempDir, nodeId, cpus, threadSiblings)
			nodeDir := filepath.Join(tempDir, fmt.Sprintf("node%d", nodeId))
			err := populateCpus(cell, nodeDir)
			Expect(err).NotTo(HaveOccurred())

			Expect(len(cell.Cpus)).To(Equal(len(cpus)))
			cpuIDs := make(map[uint32]bool)
			for _, cpu := range cell.Cpus {
				cpuIDs[cpu.Id] = true
			}
			for _, cpuID := range cpus {
				Expect(cpuIDs).To(HaveKey(uint32(cpuID)))
			}

			// Check thread siblings
			for _, cpu := range cell.Cpus {
				expectedSiblings, ok := threadSiblings[int(cpu.Id)]
				if !ok {
					expectedSiblings = []int{}
				}
				// Check that the slices cpu.Siblings and expectedSiblings contain the same elements
				Expect(len(cpu.Siblings)).To(Equal(len(expectedSiblings)), fmt.Sprintf("CPU %d: expected %d siblings, got %d", cpu.Id, len(expectedSiblings), len(cpu.Siblings)))
				siblingMap := make(map[uint32]bool)
				for _, sib := range cpu.Siblings {
					siblingMap[sib] = true
				}
				for _, expectedSib := range expectedSiblings {
					Expect(siblingMap).To(HaveKey(uint32(expectedSib)), fmt.Sprintf("CPU %d: expected sibling %d not found", cpu.Id, expectedSib))
				}
			}
		},
			Entry("no thread siblings are configured", []int{0, 1, 2, 3}, map[int][]int{}),
			Entry("thread siblings are configured", []int{0, 1, 2, 3}, map[int][]int{0: {0, 2}, 1: {1, 3}, 2: {2, 0}, 3: {3, 1}}),
		)

	})
})
