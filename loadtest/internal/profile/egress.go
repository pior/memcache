package profile

import (
	"strconv"
	"strings"
)

// EgressCapGbps returns the approximate per-VM egress bandwidth cap for a GCE
// machine type, used to compute network saturation (bytes/s ÷ cap). GCE egress
// scales at ~2 Gbps per vCPU up to a per-VM ceiling; this is a deliberately
// conservative model, not an SLA. Unknown machine types return 0 (saturation
// reported as unknown rather than wrong).
func EgressCapGbps(machineType string) float64 {
	vcpu := vcpusFromMachineType(machineType)
	if vcpu == 0 {
		return 0
	}
	const perVCPU = 2.0
	const ceiling = 32.0 // common default-tier ceiling
	cap := float64(vcpu) * perVCPU
	if cap > ceiling {
		cap = ceiling
	}
	return cap
}

// vcpusFromMachineType parses the trailing vCPU count from machine types like
// "c3-highcpu-8", "e2-standard-4", "n2-highmem-16". Shared-core types
// (e2-small, e2-micro, e2-medium) are treated as ~2 vCPU bursting.
func vcpusFromMachineType(mt string) int {
	switch mt {
	case "e2-micro", "e2-small":
		return 1
	case "e2-medium":
		return 2
	}
	i := strings.LastIndexByte(mt, '-')
	if i < 0 || i == len(mt)-1 {
		return 0
	}
	n, err := strconv.Atoi(mt[i+1:])
	if err != nil {
		return 0
	}
	return n
}
