package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func exportVirtualizationCapabilities(v VirtualizationCapabilitiesInterface, filename string) {

	machines, _ := v.GetSupportedMachineTypes()
	fmt.Println("Supported machine types:", machines)
	data, err := json.MarshalIndent(machines, "", "  ")
	if err != nil {
		panic(err)
	}

	file, err := os.Create(filename)
	if err != nil {
		panic(err)
	}
	defer file.Close()

	_, err = file.Write(data)
	if err != nil {
		panic(err)
	}

}
