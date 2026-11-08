// Package nvmlbackend reads real GPU telemetry through NVIDIA's NVML library
// (libnvidia-ml, shipped with every NVIDIA driver), using NVIDIA's go-nvml bindings.
//
// It is strictly read-only: it never changes clocks, power limits, ECC mode or any
// other GPU setting.
//
// Every NVML call returns a status code. Anything other than SUCCESS becomes a nil
// value plus a reason in Unsupported. A missing value is never invented as 0.
package nvmlbackend

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"runtime"

	"github.com/NVIDIA/go-nvml/pkg/nvml"

	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/report"
	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/telemetry"
)

// Backend is the real-GPU telemetry source.
type Backend struct {
	lib     nvml.Interface
	devices map[int]*device
	getenv  func(string) string
}

// New loads the system's NVML library.
func New() (*Backend, error) { return Open(nvml.New()) }

// Open starts a backend on the given NVML implementation. Tests pass a stand-in.
func Open(lib nvml.Interface) (*Backend, error) {
	if ret := lib.Init(); ret != nvml.SUCCESS {
		if ret == nvml.ERROR_LIBRARY_NOT_FOUND {
			return nil, errors.New("NVIDIA driver library (libnvidia-ml) not found: run this on a machine with an NVIDIA GPU and driver, or use --backend=fake")
		}
		return nil, fmt.Errorf("starting NVML: %v", ret)
	}
	return &Backend{lib: lib, devices: map[int]*device{}, getenv: os.Getenv}, nil
}

func (b *Backend) Name() string { return "nvml" }

func (b *Backend) Close() error {
	for _, d := range b.devices {
		if d.events != nil {
			d.events.Free()
		}
	}
	if ret := b.lib.Shutdown(); ret != nvml.SUCCESS {
		return fmt.Errorf("shutting down NVML: %v", ret)
	}
	return nil
}

func (b *Backend) Host() (report.Host, error) {
	h := report.Host{OS: runtime.GOOS, Provider: telemetry.DetectProvider(b.getenv)}
	h.Hostname, _ = os.Hostname()
	// These are informational; if one fails the report says "" / 0 rather than failing.
	h.DriverVersion, _ = b.lib.SystemGetDriverVersion()
	h.CUDADriverVersion, _ = b.lib.SystemGetCudaDriverVersion()
	h.NVMLVersion, _ = b.lib.SystemGetNVMLVersion()
	return h, nil
}

func (b *Backend) DeviceCount() (int, error) {
	n, ret := b.lib.DeviceGetCount()
	if ret != nvml.SUCCESS {
		return 0, fmt.Errorf("counting GPUs: %v", ret)
	}
	return n, nil
}

func (b *Backend) Device(i int) (telemetry.Device, error) {
	if d, ok := b.devices[i]; ok {
		return d, nil
	}
	h, ret := b.lib.DeviceGetHandleByIndex(i)
	if ret != nvml.SUCCESS {
		return nil, fmt.Errorf("getting handle for GPU %d: %v", i, ret)
	}
	d := &device{h: h}

	// Watch for critical XID errors with one event set per GPU, so events never need sorting by device.
	set, ret := b.lib.EventSetCreate()
	if ret == nvml.SUCCESS {
		if ret = h.RegisterEvents(nvml.EventTypeXidCriticalError, set); ret == nvml.SUCCESS {
			d.events = set
		} else {
			set.Free()
		}
	}
	if d.events == nil {
		d.eventsReason = reason(ret)
	}
	b.devices[i] = d
	return d, nil
}

type device struct {
	h            nvml.Device
	events       nvml.EventSet
	eventsReason string
}

// reason explains a failed NVML call in words.
func reason(ret nvml.Return) string {
	switch ret {
	case nvml.ERROR_NOT_SUPPORTED:
		return "not supported on this GPU or driver"
	case nvml.ERROR_NO_PERMISSION:
		return "permission denied"
	}
	return ret.Error()
}

// read records a value if the NVML call succeeded, or a reason under field if not.
func read[T any](u telemetry.Unsupported, field string, v T, ret nvml.Return) *T {
	if ret != nvml.SUCCESS {
		u[field] = reason(ret)
		return nil
	}
	return &v
}

// str is read() for identity strings, which are plain strings in the report.
func str(u telemetry.Unsupported, field string, v string, ret nvml.Return) string {
	if p := read(u, field, v, ret); p != nil {
		return *p
	}
	return ""
}

func (d *device) Identity() (report.Identity, telemetry.Unsupported, error) {
	u := telemetry.Unsupported{}
	uuid, ret := d.h.GetUUID()
	if ret != nvml.SUCCESS {
		return report.Identity{}, nil, fmt.Errorf("reading GPU UUID: %v", ret)
	}
	id := report.Identity{UUID: uuid}

	v, ret := d.h.GetSerial()
	id.Serial = str(u, "serial", v, ret)
	v, ret = d.h.GetName()
	id.Name = str(u, "name", v, ret)
	v, ret = d.h.GetBoardPartNumber()
	id.BoardPartNumber = str(u, "board_part_number", v, ret)
	v, ret = d.h.GetVbiosVersion()
	id.VBIOSVersion = str(u, "vbios_version", v, ret)

	if arch, ret := d.h.GetArchitecture(); ret == nvml.SUCCESS {
		id.Architecture = archName(arch)
	} else {
		u["architecture"] = reason(ret)
	}
	if pci, ret := d.h.GetPciInfo(); ret == nvml.SUCCESS {
		id.PCIBusID = cString(pci.BusId[:])
	} else {
		u["pci_bus_id"] = reason(ret)
	}
	if mem, ret := d.h.GetMemoryInfo(); ret == nvml.SUCCESS {
		id.MemoryTotalMiB = int64(mem.Total / (1024 * 1024))
	} else {
		u["memory_total_mib"] = reason(ret)
	}
	return id, u, nil
}

func (d *device) Memory() (report.MemoryHealth, telemetry.Unsupported) {
	u := telemetry.Unsupported{}
	var m report.MemoryHealth

	current, _, ret := d.h.GetEccMode()
	m.ECCEnabled = read(u, "ecc_enabled", current == nvml.FEATURE_ENABLED, ret)

	counts := func(counter nvml.EccCounterType, name string) report.ECCCounts {
		c, ret := d.h.GetTotalEccErrors(nvml.MEMORY_ERROR_TYPE_CORRECTED, counter)
		corrected := read(u, name+".corrected", c, ret)
		c, ret = d.h.GetTotalEccErrors(nvml.MEMORY_ERROR_TYPE_UNCORRECTED, counter)
		return report.ECCCounts{Corrected: corrected, Uncorrected: read(u, name+".uncorrected", c, ret)}
	}
	m.Volatile = counts(nvml.VOLATILE_ECC, "volatile")
	m.Aggregate = counts(nvml.AGGREGATE_ECC, "aggregate")

	// Page retirement (pre-Ampere). All three reads must work to report it.
	sbe, ret1 := d.h.GetRetiredPages(nvml.PAGE_RETIREMENT_CAUSE_MULTIPLE_SINGLE_BIT_ECC_ERRORS)
	dbe, ret2 := d.h.GetRetiredPages(nvml.PAGE_RETIREMENT_CAUSE_DOUBLE_BIT_ECC_ERROR)
	pending, ret3 := d.h.GetRetiredPagesPendingStatus()
	if failed := firstFailure(ret1, ret2, ret3); failed != nvml.SUCCESS {
		u["retired_pages"] = reason(failed)
	} else {
		m.RetiredPages = &report.RetiredPages{SingleBit: len(sbe), DoubleBit: len(dbe), PendingRetirement: pending == nvml.FEATURE_ENABLED}
	}

	// Row remapping (Ampere and later).
	corr, unc, isPending, failedRemap, ret := d.h.GetRemappedRows()
	if ret == nvml.SUCCESS {
		m.RowRemap = &report.RowRemap{Correctable: corr, Uncorrectable: unc, Pending: isPending, Failed: failedRemap}
	} else {
		u["row_remap"] = reason(ret)
	}
	return m, u
}

func (d *device) Interconnect() (report.Interconnect, telemetry.Unsupported) {
	u := telemetry.Unsupported{}
	var ic report.Interconnect
	v, ret := d.h.GetCurrPcieLinkGeneration()
	ic.PCIeGenCurrent = read(u, "pcie_gen_current", v, ret)
	v, ret = d.h.GetMaxPcieLinkGeneration()
	ic.PCIeGenMax = read(u, "pcie_gen_max", v, ret)
	v, ret = d.h.GetCurrPcieLinkWidth()
	ic.PCIeWidthCurrent = read(u, "pcie_width_current", v, ret)
	v, ret = d.h.GetMaxPcieLinkWidth()
	ic.PCIeWidthMax = read(u, "pcie_width_max", v, ret)
	v, ret = d.h.GetPcieReplayCounter()
	ic.PCIeReplayCounter = read(u, "pcie_replay_counter", uint64(v), ret)
	return ic, u
}

func (d *device) Limits() (report.Limits, telemetry.Unsupported) {
	u := telemetry.Unsupported{}
	var l report.Limits
	v, ret := d.h.GetEnforcedPowerLimit()
	l.PowerLimitMW = read(u, "power_limit_mw", int64(v), ret)
	v, ret = d.h.GetMaxClockInfo(nvml.CLOCK_SM)
	l.MaxSMClockMHz = read(u, "max_sm_clock_mhz", int64(v), ret)
	v, ret = d.h.GetMaxClockInfo(nvml.CLOCK_MEM)
	l.MaxMemClockMHz = read(u, "max_mem_clock_mhz", int64(v), ret)
	v, ret = d.h.GetTemperatureThreshold(nvml.TEMPERATURE_THRESHOLD_SLOWDOWN)
	l.SlowdownTempC = read(u, "slowdown_temp_c", int64(v), ret)
	v, ret = d.h.GetTemperatureThreshold(nvml.TEMPERATURE_THRESHOLD_SHUTDOWN)
	l.ShutdownTempC = read(u, "shutdown_temp_c", int64(v), ret)
	return l, u
}

func (d *device) Sample() (report.Sample, telemetry.Unsupported) {
	u := telemetry.Unsupported{}
	var s report.Sample
	v, ret := d.h.GetTemperature(nvml.TEMPERATURE_GPU)
	s.TempC = read(u, "temp_c", int64(v), ret)
	v, ret = d.h.GetPowerUsage()
	s.PowerMW = read(u, "power_mw", int64(v), ret)
	v, ret = d.h.GetClockInfo(nvml.CLOCK_SM)
	s.SMClockMHz = read(u, "sm_clock_mhz", int64(v), ret)
	v, ret = d.h.GetClockInfo(nvml.CLOCK_MEM)
	s.MemClockMHz = read(u, "mem_clock_mhz", int64(v), ret)
	if util, ret := d.h.GetUtilizationRates(); ret == nvml.SUCCESS {
		s.UtilGPUPct, s.UtilMemPct = report.Ptr(int64(util.Gpu)), report.Ptr(int64(util.Memory))
	} else {
		u["util_gpu_pct"], u["util_mem_pct"] = reason(ret), reason(ret)
	}
	// Newer drivers call these "clocks event reasons"; older ones "throttle reasons".
	mask, ret := d.h.GetCurrentClocksEventReasons()
	if ret != nvml.SUCCESS {
		mask, ret = d.h.GetCurrentClocksThrottleReasons()
	}
	if ret == nvml.SUCCESS {
		s.ThrottleReasons = telemetry.ThrottleReasons(mask)
	} else {
		u["throttle_reasons"] = reason(ret)
	}
	return s, u
}

func (d *device) PollEvents() ([]report.XIDEvent, telemetry.Unsupported) {
	if d.events == nil {
		return nil, telemetry.Unsupported{"events": d.eventsReason}
	}
	var out []report.XIDEvent
	for range 64 { // drain what's queued, but never loop forever
		e, ret := d.events.Wait(0) // 0 ms: don't block
		if ret != nvml.SUCCESS {   // ERROR_TIMEOUT = nothing more queued
			break
		}
		if e.EventType == nvml.EventTypeXidCriticalError {
			out = append(out, report.XIDEvent{XID: e.EventData, Description: telemetry.XIDDescription(e.EventData)})
		}
	}
	return out, nil
}

func firstFailure(rets ...nvml.Return) nvml.Return {
	for _, r := range rets {
		if r != nvml.SUCCESS {
			return r
		}
	}
	return nvml.SUCCESS
}

// cString converts a NUL-terminated C char array to a Go string.
func cString(b []int8) string {
	out := make([]byte, 0, len(b))
	for _, c := range b {
		if c == 0 {
			break
		}
		out = append(out, byte(c))
	}
	return string(bytes.TrimSpace(out))
}

func archName(a nvml.DeviceArchitecture) string {
	switch a {
	case nvml.DEVICE_ARCH_KEPLER:
		return "Kepler"
	case nvml.DEVICE_ARCH_MAXWELL:
		return "Maxwell"
	case nvml.DEVICE_ARCH_PASCAL:
		return "Pascal"
	case nvml.DEVICE_ARCH_VOLTA:
		return "Volta"
	case nvml.DEVICE_ARCH_TURING:
		return "Turing"
	case nvml.DEVICE_ARCH_AMPERE:
		return "Ampere"
	case nvml.DEVICE_ARCH_ADA:
		return "Ada"
	case nvml.DEVICE_ARCH_HOPPER:
		return "Hopper"
	case nvml.DEVICE_ARCH_BLACKWELL:
		return "Blackwell"
	}
	return fmt.Sprintf("unknown (%d)", a)
}
