package telemetry

import "fmt"

// xidDescriptions are short meanings of common XID codes, from NVIDIA's
// "XID Errors" documentation. XIDs are the driver's error codes for a GPU.
var xidDescriptions = map[uint64]string{
	13:  "Graphics engine exception",
	31:  "GPU memory page fault",
	43:  "GPU stopped processing",
	45:  "Preemptive cleanup, due to previous errors",
	48:  "Double-bit ECC error",
	61:  "Internal micro-controller breakpoint/warning",
	62:  "Internal micro-controller halt",
	63:  "ECC page retirement or row remapping recording event",
	64:  "ECC page retirement or row remapper recording failure",
	74:  "NVLink error",
	79:  "GPU has fallen off the bus",
	92:  "High single-bit ECC error rate",
	94:  "Contained ECC error",
	95:  "Uncontained ECC error",
	119: "GSP RPC timeout",
	120: "GSP error",
}

// XIDDescription returns a human-readable meaning for an XID code.
func XIDDescription(xid uint64) string {
	if d, ok := xidDescriptions[xid]; ok {
		return d
	}
	return fmt.Sprintf("XID %d (see NVIDIA XID documentation)", xid)
}
