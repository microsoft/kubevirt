package main

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	virt_capabilities "kubevirt.io/kubevirt/pkg/virt-launcher-common/virt-capabilities"
)

const (
	XmlBasePath = "/var/lib/kubevirt-node-labeller"
)

func executeCommand(command string) (string, error) {
	cmd := exec.Command("bash", "-c", command)
	output, err := cmd.Output()
	outputStr := strings.TrimSpace(string(output))
	if err != nil {
		return "", fmt.Errorf("failed to execute command %s: %w", command, err)
	}
	return outputStr, nil
}

func queryMachineArchitecture() (string, error) {
	return executeCommand("uname -m")
}

func queryKvmMinor() (string, error) {
	return executeCommand("grep -w 'kvm' /proc/misc | cut -f 1 -d' '")
}

func main() {

	machine := "q35"

	arch, err := queryMachineArchitecture()
	if err != nil {
		fmt.Printf("Error querying machine architecture: %v\n", err)
		return
	}

	fmt.Printf("Detected arch=\"%s\"\n", arch)

	if arch == "aarch64" {
		machine = "virt"
	} else if arch == "s390x" {
		machine = "390-ccw-virtio"
	} else if arch != "x86_64" {
		// Node labeling cannot proceed for this architecture
		fmt.Printf("Node labeling cannot proceed for architecture: %s\n", arch)
		return
	}

	kvmMinor, err := queryKvmMinor()
	if err != nil {
		fmt.Printf("Error querying KVM minor: %v\n", err)
		return
	}

	virttype := "qemu"

	_, err = exec.Command("ls", "/dev/kvm").Output()
	kvmExists := err == nil

	if !kvmExists && kvmMinor != "" {
		executeCommand("mknod /dev/kvm c 10 " + kvmMinor)
	}

	_, err = exec.Command("ls", "/dev/kvm").Output()
	kvmExists = err == nil

	if kvmExists {
		executeCommand("chmod o+rw /dev/kvm")
		virttype = "kvm"
	}

	_, err = exec.Command("ls", "/dev/sev").Output()
	sevExists := err == nil

	if sevExists {
		// QEMU requires RW access to query SEV capabilities
		executeCommand("chmod o+rw /dev/kvm")
	}

	cmd := exec.Command("virtqemud", "-d")
	err = cmd.Start()
	if err != nil {
		fmt.Printf("Failed to start virtqemud: %v\n", err)
		return
	}
	fmt.Println("virtqemud started in daemon mode")

	executeCommand(fmt.Sprintf("mkdir -p %s", XmlBasePath)) // TODO Remove this later, this is just for testing

	fmt.Println("Waiting for virtqemud to start...")
	time.Sleep(5 * time.Second) // Wait for virtqemud to start

	_, err = executeCommand(fmt.Sprintf("virsh domcapabilities --machine %s --arch %s --virttype %s > %s/virsh_domcapabilities.xml", machine, arch, virttype, XmlBasePath))

	if err != nil {
		fmt.Printf("Failed to get domain capabilities: %v\n", err)
		return
	}

	if arch == "x86_64" || arch == "s390x" {
		cmd := fmt.Sprintf("virsh domcapabilities --machine %s --arch %s --virttype %s | virsh hypervisor-cpu-baseline --features /dev/stdin --machine %s --arch %s --virttype %s > %s/supported_features.xml", machine, arch, virttype, machine, arch, virttype, XmlBasePath)
		_, err := executeCommand(cmd)
		if err != nil {
			fmt.Printf("Failed to get supported features: %v\n", err)
			return
		}
	}

	_, err = executeCommand(fmt.Sprintf("virsh capabilities > %s/capabilities.xml", XmlBasePath))
	if err != nil {
		fmt.Printf("Failed to get node capabilities: %v\n", err)
		return
	}

	capabilityExtractor := NewVirtualizationCapabilitiesLibvirtQemu(fmt.Sprintf("%s/supported_features.xml", XmlBasePath), fmt.Sprintf("%s/virsh_domcapabilities.xml", XmlBasePath), fmt.Sprintf("%s/capabilities.xml", XmlBasePath))

	virt_capabilities.ExportVirtualizationCapabilities(capabilityExtractor, fmt.Sprintf("%s/virtualization_capabilities.json", XmlBasePath))
}
