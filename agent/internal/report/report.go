// Package report defines the Assay health report: the JSON document the agent
// writes for each check, and that lenders, buyers or anyone else can read and verify.
//
// Two rules shape this schema:
//
//  1. Unknown is not zero. A value the GPU cannot report is nil (JSON null) and the
//     reason goes in GPU.Unsupported. "0 errors" and "couldn't check" must never look alike.
//  2. Integers only. Power is in milliwatts, clocks in MHz, time in milliseconds.
//     Reports get signed byte-for-byte, and integers have exactly one JSON spelling.
package report

// SchemaVersion is bumped whenever a field changes meaning or is removed.
const SchemaVersion = 1

// Report is one health check of every GPU on one machine.
type Report struct {
	SchemaVersion int    `json:"schema_version"`
	ReportID      string `json:"report_id"`  // random 128-bit hex ID
	CreatedAt     string `json:"created_at"` // RFC 3339, UTC
	Agent         Agent  `json:"agent"`
	Host          Host   `json:"host"`
	GPUs          []GPU  `json:"gpus"`
	// Signature covers every other field. It is left out of the bytes it signs.
	Signature *Signature `json:"signature,omitempty"`
}

// Signature proves the report hasn't changed since the agent wrote it.
type Signature struct {
	Algorithm string `json:"algorithm"`  // always "ed25519"
	KeyID     string `json:"key_id"`     // short fingerprint of the public key
	PublicKey string `json:"public_key"` // base64, 32 bytes
	Digest    string `json:"digest"`     // "sha256:<hex>" of the signed bytes; the log will use this
	Value     string `json:"value"`      // base64 Ed25519 signature over the signed bytes
}

// Agent describes the software that produced the report.
type Agent struct {
	Version  string `json:"version"`
	Backend  string `json:"backend"`            // "nvml" (real GPU) or "fake"
	Scenario string `json:"scenario,omitempty"` // fake backend only
}

// Host describes the machine and driver stack.
type Host struct {
	Hostname          string `json:"hostname"`
	OS                string `json:"os"`
	Provider          string `json:"provider"` // "colab", "kaggle" or "unknown"
	DriverVersion     string `json:"driver_version"`
	CUDADriverVersion int    `json:"cuda_driver_version"` // e.g. 12040 = CUDA 12.4
	NVMLVersion       string `json:"nvml_version"`
}

// GPU is everything recorded about one physical GPU.
type GPU struct {
	Index        int          `json:"index"`
	Identity     Identity     `json:"identity"`
	Memory       MemoryHealth `json:"memory"`
	Interconnect Interconnect `json:"interconnect"`
	Limits       Limits       `json:"limits"`
	Samples      []Sample     `json:"samples"`
	Events       []XIDEvent   `json:"events"`
	// Unsupported maps a field name to the reason it could not be read.
	Unsupported map[string]string `json:"unsupported,omitempty"`
}

// Identity tells physical GPUs apart. Serial + UUID is how the census decides
// whether it has seen a GPU before.
type Identity struct {
	UUID            string `json:"uuid"`
	Serial          string `json:"serial"`
	Name            string `json:"name"`
	Architecture    string `json:"architecture"`
	PCIBusID        string `json:"pci_bus_id"`
	BoardPartNumber string `json:"board_part_number"`
	VBIOSVersion    string `json:"vbios_version"`
	MemoryTotalMiB  int64  `json:"memory_total_mib"`
}

// MemoryHealth covers ECC errors and memory the GPU has retired.
type MemoryHealth struct {
	ECCEnabled *bool `json:"ecc_enabled"`
	// Volatile counts reset at driver reload; Aggregate counts are lifetime.
	Volatile  ECCCounts `json:"volatile"`
	Aggregate ECCCounts `json:"aggregate"`
	// RetiredPages is used by pre-Ampere GPUs (T4, P100, V100).
	RetiredPages *RetiredPages `json:"retired_pages"`
	// RowRemap replaced page retirement from Ampere (A100) on.
	RowRemap *RowRemap `json:"row_remap"`
}

// ECCCounts: corrected = single-bit, fixed silently; uncorrected = double-bit, data lost.
type ECCCounts struct {
	Corrected   *uint64 `json:"corrected"`
	Uncorrected *uint64 `json:"uncorrected"`
}

// RetiredPages counts memory pages the GPU stopped using.
type RetiredPages struct {
	SingleBit         int  `json:"single_bit"`         // retired after repeated single-bit errors
	DoubleBit         int  `json:"double_bit"`         // retired after a double-bit error
	PendingRetirement bool `json:"pending_retirement"` // takes effect at next driver reload
}

// RowRemap counts HBM rows replaced with spare rows.
type RowRemap struct {
	Correctable   int  `json:"correctable"`
	Uncorrectable int  `json:"uncorrectable"`
	Pending       bool `json:"pending"`
	Failed        bool `json:"failed"` // ran out of spare rows: the GPU should be serviced
}

// Interconnect is the GPU's link to the host (and to other GPUs).
type Interconnect struct {
	PCIeGenCurrent    *int    `json:"pcie_gen_current"`
	PCIeGenMax        *int    `json:"pcie_gen_max"`
	PCIeWidthCurrent  *int    `json:"pcie_width_current"`
	PCIeWidthMax      *int    `json:"pcie_width_max"`
	PCIeReplayCounter *uint64 `json:"pcie_replay_counter"` // link-level retransmits
}

// Limits are the GPU's configured ceilings, used to judge samples against.
type Limits struct {
	PowerLimitMW   *int64 `json:"power_limit_mw"`
	MaxSMClockMHz  *int64 `json:"max_sm_clock_mhz"`
	MaxMemClockMHz *int64 `json:"max_mem_clock_mhz"`
	SlowdownTempC  *int64 `json:"slowdown_temp_c"`
	ShutdownTempC  *int64 `json:"shutdown_temp_c"`
}

// Sample is one reading of the fast-changing values, taken every interval.
type Sample struct {
	TMs             int64    `json:"t_ms"` // milliseconds since collection started
	TempC           *int64   `json:"temp_c"`
	PowerMW         *int64   `json:"power_mw"`
	SMClockMHz      *int64   `json:"sm_clock_mhz"`
	MemClockMHz     *int64   `json:"mem_clock_mhz"`
	UtilGPUPct      *int64   `json:"util_gpu_pct"`
	UtilMemPct      *int64   `json:"util_mem_pct"`
	ThrottleReasons []string `json:"throttle_reasons"` // empty = running unthrottled
}

// XIDEvent is an error the NVIDIA driver reported for this GPU.
type XIDEvent struct {
	TMs         int64  `json:"t_ms"`
	XID         uint64 `json:"xid"`
	Description string `json:"description"`
}

// Ptr returns a pointer to v. Handy for filling optional fields: report.Ptr(int64(42)).
func Ptr[T any](v T) *T { return &v }
