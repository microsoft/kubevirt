package main

import (
	"fmt"

	nodelabeller "kubevirt.io/kubevirt/pkg/virt-handler/node-labeller"
)

func main() {
	fmt.Println("Testing node topology reading logic!")
	fmt.Println(nodelabeller.ReadNodeTopology())
}
