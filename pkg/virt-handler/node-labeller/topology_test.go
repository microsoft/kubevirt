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
)

/*
4. Read distances properly
5. Read CPUs properly
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
})
