package api

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/resource"
	v1 "kubevirt.io/api/core/v1"

	k8sv1 "k8s.io/api/core/v1"
	"kubevirt.io/client-go/log"
)

const (
	MultiQueueMaxQueues = uint32(256)
)

func CalculateNetworkQueues(vmi *v1.VirtualMachineInstance, ifaceType string) uint32 {
	if ifaceType != v1.VirtIO {
		return 0
	}
	return NetworkQueuesCapacity(vmi)
}

func NetworkQueuesCapacity(vmi *v1.VirtualMachineInstance) uint32 {
	if !isTrue(vmi.Spec.Domain.Devices.NetworkInterfaceMultiQueue) {
		return 0
	}

	cpuTopology := GetCPUTopology(vmi)
	queueNumber := CalculateRequestedVCPUs(cpuTopology)

	if queueNumber > MultiQueueMaxQueues {
		log.Log.V(3).Infof("Capped the number of queues to be the current maximum of tap device queues: %d", MultiQueueMaxQueues)
		queueNumber = MultiQueueMaxQueues
	}
	return queueNumber
}

func isTrue(networkInterfaceMultiQueue *bool) bool {
	return (networkInterfaceMultiQueue != nil) && (*networkInterfaceMultiQueue)
}

func GetCPUTopology(vmi *v1.VirtualMachineInstance) *CPUTopology {
	cores := uint32(1)
	threads := uint32(1)
	sockets := uint32(1)
	vmiCPU := vmi.Spec.Domain.CPU
	if vmiCPU != nil {
		if vmiCPU.Cores != 0 {
			cores = vmiCPU.Cores
		}

		if vmiCPU.Threads != 0 {
			threads = vmiCPU.Threads
		}

		if vmiCPU.Sockets != 0 {
			sockets = vmiCPU.Sockets
		}
	}
	// A default guest CPU topology is being set in API mutator webhook, if nothing provided by a user.
	// However this setting is still required to handle situations when the webhook fails to set a default topology.
	if vmiCPU == nil || (vmiCPU.Cores == 0 && vmiCPU.Sockets == 0 && vmiCPU.Threads == 0) {
		//if cores, sockets, threads are not set, take value from domain resources request or limits and
		//set value into sockets, which have best performance (https://bugzilla.redhat.com/show_bug.cgi?id=1653453)
		resources := vmi.Spec.Domain.Resources
		if cpuLimit, ok := resources.Limits[k8sv1.ResourceCPU]; ok {
			sockets = uint32(cpuLimit.Value())
		} else if cpuRequests, ok := resources.Requests[k8sv1.ResourceCPU]; ok {
			sockets = uint32(cpuRequests.Value())
		}
	}

	return &CPUTopology{
		Sockets: sockets,
		Cores:   cores,
		Threads: threads,
	}
}

func QuantityToByte(quantity resource.Quantity) (Memory, error) {
	memorySize, isInt := quantity.AsInt64()
	if !isInt {
		memorySize = quantity.Value() - 1
	}

	if memorySize < 0 {
		return Memory{Unit: "b"}, fmt.Errorf("Memory size '%s' must be greater than or equal to 0", quantity.String())
	}
	return Memory{
		Value: uint64(memorySize),
		Unit:  "b",
	}, nil
}

func QuantityToMebiByte(quantity resource.Quantity) (uint64, error) {
	bytes, err := QuantityToByte(quantity)
	if err != nil {
		return 0, err
	}
	if bytes.Value == 0 {
		return 0, nil
	} else if bytes.Value < 1048576 {
		return 1, nil
	}
	return uint64(float64(bytes.Value)/1048576 + 0.5), nil
}

func CalculateRequestedVCPUs(cpuTopology *CPUTopology) uint32 {
	return cpuTopology.Cores * cpuTopology.Sockets * cpuTopology.Threads
}
