package nvmlbackend

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"

	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/telemetry"
)

// These stand-ins imitate NVML well enough to test our translation code without a GPU.
// Each embeds the real interface: any method we didn't override panics if called,
// so a test can't silently pass by hitting something unexpected.

type stubLib struct {
	nvml.Interface
	initRet  nvml.Return
	dev      *stubDevice
	shutdown bool
}

func (l *stubLib) Init() nvml.Return                             { return l.initRet }
func (l *stubLib) Shutdown() nvml.Return                         { l.shutdown = true; return nvml.SUCCESS }
func (l *stubLib) SystemGetDriverVersion() (string, nvml.Return) { return "550.54.15", nvml.SUCCESS }
func (l *stubLib) SystemGetCudaDriverVersion() (int, nvml.Return) {
	return 12040, nvml.SUCCESS
}
func (l *stubLib) SystemGetNVMLVersion() (string, nvml.Return) { return "12.550.54.15", nvml.SUCCESS }
func (l *stubLib) DeviceGetCount() (int, nvml.Return)          { return 1, nvml.SUCCESS }
func (l *stubLib) DeviceGetHandleByIndex(int) (nvml.Device, nvml.Return) {
	return l.dev, nvml.SUCCESS
}
func (l *stubLib) EventSetCreate() (nvml.EventSet, nvml.Return) {
	return &stubEvents{queue: l.dev.xids}, nvml.SUCCESS
}

// stubDevice looks like a Tesla T4: page retirement works, row remapping doesn't.
type stubDevice struct {
	nvml.Device
	registerRet nvml.Return
	throttle    uint64
	xids        []uint64
}

func ok[T any](v T) (T, nvml.Return) { return v, nvml.SUCCESS }

func (d *stubDevice) GetUUID() (string, nvml.Return)   { return ok("GPU-1234") }
func (d *stubDevice) GetSerial() (string, nvml.Return) { return ok("1320220012345") }
func (d *stubDevice) GetName() (string, nvml.Return)   { return ok("Tesla T4") }
func (d *stubDevice) GetBoardPartNumber() (string, nvml.Return) {
	return "", nvml.ERROR_NOT_SUPPORTED // cloud VMs often hide this
}
func (d *stubDevice) GetVbiosVersion() (string, nvml.Return) { return ok("90.04.38.00.03") }
func (d *stubDevice) GetArchitecture() (nvml.DeviceArchitecture, nvml.Return) {
	return ok(nvml.DeviceArchitecture(nvml.DEVICE_ARCH_TURING))
}
func (d *stubDevice) GetPciInfo() (nvml.PciInfo, nvml.Return) {
	var p nvml.PciInfo
	for i, c := range "00000000:00:04.0" {
		p.BusId[i] = int8(c)
	}
	return ok(p)
}
func (d *stubDevice) GetMemoryInfo() (nvml.Memory, nvml.Return) {
	return ok(nvml.Memory{Total: 15360 * 1024 * 1024})
}
func (d *stubDevice) GetEccMode() (nvml.EnableState, nvml.EnableState, nvml.Return) {
	return nvml.FEATURE_ENABLED, nvml.FEATURE_ENABLED, nvml.SUCCESS
}
func (d *stubDevice) GetTotalEccErrors(t nvml.MemoryErrorType, c nvml.EccCounterType) (uint64, nvml.Return) {
	if t == nvml.MEMORY_ERROR_TYPE_CORRECTED && c == nvml.AGGREGATE_ECC {
		return ok(uint64(17))
	}
	return ok(uint64(0))
}
func (d *stubDevice) GetRetiredPages(c nvml.PageRetirementCause) ([]uint64, nvml.Return) {
	if c == nvml.PAGE_RETIREMENT_CAUSE_MULTIPLE_SINGLE_BIT_ECC_ERRORS {
		return ok([]uint64{0xdead000, 0xbeef000})
	}
	return ok([]uint64{})
}
func (d *stubDevice) GetRetiredPagesPendingStatus() (nvml.EnableState, nvml.Return) {
	return ok(nvml.EnableState(nvml.FEATURE_DISABLED))
}
func (d *stubDevice) GetRemappedRows() (int, int, bool, bool, nvml.Return) {
	return 0, 0, false, false, nvml.ERROR_NOT_SUPPORTED
}
func (d *stubDevice) GetCurrPcieLinkGeneration() (int, nvml.Return) { return ok(3) }
func (d *stubDevice) GetMaxPcieLinkGeneration() (int, nvml.Return)  { return ok(3) }
func (d *stubDevice) GetCurrPcieLinkWidth() (int, nvml.Return)      { return ok(16) }
func (d *stubDevice) GetMaxPcieLinkWidth() (int, nvml.Return)       { return ok(16) }
func (d *stubDevice) GetPcieReplayCounter() (int, nvml.Return)      { return ok(2) }
func (d *stubDevice) GetEnforcedPowerLimit() (uint32, nvml.Return)  { return ok(uint32(70000)) }
func (d *stubDevice) GetMaxClockInfo(c nvml.ClockType) (uint32, nvml.Return) {
	if c == nvml.CLOCK_SM {
		return ok(uint32(1590))
	}
	return ok(uint32(5001))
}
func (d *stubDevice) GetTemperatureThreshold(t nvml.TemperatureThresholds) (uint32, nvml.Return) {
	if t == nvml.TEMPERATURE_THRESHOLD_SLOWDOWN {
		return ok(uint32(90))
	}
	return ok(uint32(96))
}
func (d *stubDevice) GetTemperature(nvml.TemperatureSensors) (uint32, nvml.Return) {
	return ok(uint32(41))
}
func (d *stubDevice) GetPowerUsage() (uint32, nvml.Return) { return ok(uint32(9876)) }
func (d *stubDevice) GetClockInfo(c nvml.ClockType) (uint32, nvml.Return) {
	if c == nvml.CLOCK_SM {
		return ok(uint32(300))
	}
	return ok(uint32(405))
}
func (d *stubDevice) GetUtilizationRates() (nvml.Utilization, nvml.Return) {
	return ok(nvml.Utilization{Gpu: 0, Memory: 0})
}
func (d *stubDevice) GetCurrentClocksEventReasons() (uint64, nvml.Return) { return ok(d.throttle) }
func (d *stubDevice) RegisterEvents(uint64, nvml.EventSet) nvml.Return    { return d.registerRet }

type stubEvents struct {
	nvml.EventSet
	queue []uint64
	freed bool
}

func (e *stubEvents) Wait(uint32) (nvml.EventData, nvml.Return) {
	if len(e.queue) == 0 {
		return nvml.EventData{}, nvml.ERROR_TIMEOUT
	}
	x := e.queue[0]
	e.queue = e.queue[1:]
	return nvml.EventData{EventType: nvml.EventTypeXidCriticalError, EventData: x}, nvml.SUCCESS
}
func (e *stubEvents) Free() nvml.Return { e.freed = true; return nvml.SUCCESS }

func newT4() *stubLib {
	return &stubLib{initRet: nvml.SUCCESS, dev: &stubDevice{registerRet: nvml.SUCCESS, throttle: 0x1}}
}

func collect(t *testing.T, lib *stubLib) *Backend {
	t.Helper()
	b, err := Open(lib)
	if err != nil {
		t.Fatal(err)
	}
	b.getenv = func(k string) string {
		if k == "COLAB_RELEASE_TAG" {
			return "release-colab"
		}
		return ""
	}
	return b
}

func TestMissingDriverGivesAFriendlyError(t *testing.T) {
	_, err := Open(&stubLib{initRet: nvml.ERROR_LIBRARY_NOT_FOUND})
	if err == nil || !strings.Contains(err.Error(), "--backend=fake") {
		t.Fatalf("got %v", err)
	}
}

func TestReadsAT4(t *testing.T) {
	lib := newT4()
	b := collect(t, lib)
	host, _ := b.Host()
	if host.Provider != "colab" || host.DriverVersion != "550.54.15" || host.CUDADriverVersion != 12040 {
		t.Errorf("host = %+v", host)
	}

	gpus, err := telemetry.Collect(b, telemetry.Options{Samples: 2, Sleep: func(time.Duration) {}})
	if err != nil {
		t.Fatal(err)
	}
	g := gpus[0]

	id := g.Identity
	if id.UUID != "GPU-1234" || id.Name != "Tesla T4" || id.Architecture != "Turing" ||
		id.PCIBusID != "00000000:00:04.0" || id.MemoryTotalMiB != 15360 {
		t.Errorf("identity = %+v", id)
	}
	if id.BoardPartNumber != "" || g.Unsupported["board_part_number"] == "" {
		t.Error("unsupported board part number should be empty with a reason")
	}

	m := g.Memory
	if !*m.ECCEnabled || *m.Aggregate.Corrected != 17 || *m.Volatile.Uncorrected != 0 {
		t.Errorf("ECC = enabled %v, %+v", *m.ECCEnabled, m.Aggregate)
	}
	if m.RetiredPages == nil || m.RetiredPages.SingleBit != 2 || m.RetiredPages.DoubleBit != 0 {
		t.Errorf("retired pages = %+v", m.RetiredPages)
	}
	if m.RowRemap != nil || g.Unsupported["row_remap"] != "not supported on this GPU or driver" {
		t.Errorf("row remap should be null with a reason, got %+v / %q", m.RowRemap, g.Unsupported["row_remap"])
	}

	if *g.Interconnect.PCIeGenCurrent != 3 || *g.Interconnect.PCIeReplayCounter != 2 {
		t.Errorf("interconnect = %+v", g.Interconnect)
	}
	if *g.Limits.PowerLimitMW != 70000 || *g.Limits.SlowdownTempC != 90 {
		t.Errorf("limits = %+v", g.Limits)
	}

	s := g.Samples[0]
	if *s.TempC != 41 || *s.PowerMW != 9876 || *s.SMClockMHz != 300 || !slices.Equal(s.ThrottleReasons, []string{"gpu_idle"}) {
		t.Errorf("sample = temp %d power %d sm %d reasons %v", *s.TempC, *s.PowerMW, *s.SMClockMHz, s.ThrottleReasons)
	}
	if g.Events == nil || len(g.Events) != 0 {
		t.Errorf("events = %v, want [] (watched, none)", g.Events)
	}

	if err := b.Close(); err != nil || !lib.shutdown {
		t.Error("Close should shut NVML down")
	}
}

func TestXIDEventsAreCaptured(t *testing.T) {
	lib := newT4()
	lib.dev.xids = []uint64{79, 48}
	gpus, err := telemetry.Collect(collect(t, lib), telemetry.Options{Samples: 1, Sleep: func(time.Duration) {}})
	if err != nil {
		t.Fatal(err)
	}
	ev := gpus[0].Events
	if len(ev) != 2 || ev[0].XID != 79 || ev[0].Description != "GPU has fallen off the bus" || ev[1].XID != 48 {
		t.Fatalf("events = %+v", ev)
	}
}

func TestUnwatchableEventsAreNullNotEmpty(t *testing.T) {
	lib := newT4()
	lib.dev.registerRet = nvml.ERROR_NO_PERMISSION
	gpus, _ := telemetry.Collect(collect(t, lib), telemetry.Options{Samples: 1, Sleep: func(time.Duration) {}})
	g := gpus[0]
	if g.Events != nil || g.Unsupported["events"] != "permission denied" {
		t.Fatalf("events = %v, reason %q; want null + 'permission denied'", g.Events, g.Unsupported["events"])
	}
}

func TestCString(t *testing.T) {
	in := []int8{'a', 'b', 0, 'z'}
	if got := cString(in); got != "ab" {
		t.Fatalf("got %q", got)
	}
}
