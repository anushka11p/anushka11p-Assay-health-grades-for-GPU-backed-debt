// Package fake is a pretend GPU backend. It imitates an NVIDIA Tesla T4 (the GPU
// free Colab gives you) running under full load, and can act out common hardware
// problems on purpose. Everything is deterministic: the same scenario always
// produces the same data, so tests can compare exact output.
package fake

import (
	"fmt"
	"sort"
	"strings"

	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/report"
	"github.com/anushka11p/Assay-health-grades-for-GPU-backed-debt/agent/internal/telemetry"
)

// scenarios lists each scenario and what it acts out.
var scenarios = map[string]string{
	"healthy":          "a T4 in good condition",
	"hbm-degrading":    "memory wearing out: many corrected ECC errors, some uncorrected, retired pages",
	"thermal-throttle": "poor cooling: heats past its slowdown temperature and cuts its clocks",
	"xid-storm":        "unstable GPU: the driver reports a burst of XID errors",
	"pcie-flaky":       "bad slot or riser: PCIe link trained at Gen1 x8 with many retransmits",
}

// Scenarios returns the scenario names, sorted.
func Scenarios() []string {
	names := make([]string, 0, len(scenarios))
	for n := range scenarios {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Backend is the fake telemetry source. It has one GPU.
type Backend struct {
	scenario string
	device   *device
}

// New returns a fake backend acting out the named scenario.
func New(scenario string) (*Backend, error) {
	if _, ok := scenarios[scenario]; !ok {
		return nil, fmt.Errorf("unknown scenario %q (choose from: %s)", scenario, strings.Join(Scenarios(), ", "))
	}
	return &Backend{scenario: scenario, device: &device{scenario: scenario}}, nil
}

func (b *Backend) Name() string     { return "fake" }
func (b *Backend) Scenario() string { return b.scenario }
func (b *Backend) Close() error     { return nil }

func (b *Backend) DeviceCount() (int, error) { return 1, nil }

func (b *Backend) Device(i int) (telemetry.Device, error) {
	if i != 0 {
		return nil, fmt.Errorf("fake backend has 1 GPU, asked for index %d", i)
	}
	return b.device, nil
}

func (b *Backend) Host() (report.Host, error) {
	return report.Host{
		Hostname:          "fake-host",
		OS:                "linux",
		Provider:          "unknown",
		DriverVersion:     "550.54.15",
		CUDADriverVersion: 12040,
		NVMLVersion:       "12.550.54.15",
	}, nil
}

// device is the single fake T4. sampleTick and eventTick count calls, so values
// can change over time (e.g. temperature rising) in a repeatable way.
type device struct {
	scenario   string
	sampleTick int
	eventTick  int
}

func (d *device) Identity() (report.Identity, telemetry.Unsupported, error) {
	// Each scenario gets its own serial/UUID, so they look like different physical GPUs.
	n := 0
	for i, s := range Scenarios() {
		if s == d.scenario {
			n = i + 1
		}
	}
	return report.Identity{
		UUID:            fmt.Sprintf("GPU-fa4e0000-0000-4000-8000-%012d", n),
		Serial:          fmt.Sprintf("FAKE%09d", n),
		Name:            "Tesla T4 (fake)",
		Architecture:    "Turing",
		PCIBusID:        "00000000:00:04.0",
		BoardPartNumber: "900-2G183-0000-001",
		VBIOSVersion:    "90.04.38.00.03",
		MemoryTotalMiB:  15360,
	}, nil, nil
}

func (d *device) Memory() (report.MemoryHealth, telemetry.Unsupported) {
	m := report.MemoryHealth{
		ECCEnabled:   report.Ptr(true),
		Volatile:     report.ECCCounts{Corrected: report.Ptr(uint64(0)), Uncorrected: report.Ptr(uint64(0))},
		Aggregate:    report.ECCCounts{Corrected: report.Ptr(uint64(3)), Uncorrected: report.Ptr(uint64(0))},
		RetiredPages: &report.RetiredPages{},
	}
	switch d.scenario {
	case "hbm-degrading":
		m.Volatile = report.ECCCounts{Corrected: report.Ptr(uint64(1287)), Uncorrected: report.Ptr(uint64(0))}
		m.Aggregate = report.ECCCounts{Corrected: report.Ptr(uint64(48211)), Uncorrected: report.Ptr(uint64(2))}
		m.RetiredPages = &report.RetiredPages{SingleBit: 6, DoubleBit: 2, PendingRetirement: true}
	case "xid-storm":
		// The XID 48 (double-bit error) in this scenario also shows up in the counters.
		m.Volatile.Uncorrected = report.Ptr(uint64(1))
		m.Aggregate.Uncorrected = report.Ptr(uint64(1))
	}
	// Turing GPUs retire whole pages; row remapping only exists from Ampere on.
	return m, telemetry.Unsupported{"row_remap": "not supported on Turing (uses page retirement)"}
}

func (d *device) Interconnect() (report.Interconnect, telemetry.Unsupported) {
	ic := report.Interconnect{
		PCIeGenCurrent:    report.Ptr(3),
		PCIeGenMax:        report.Ptr(3),
		PCIeWidthCurrent:  report.Ptr(16),
		PCIeWidthMax:      report.Ptr(16),
		PCIeReplayCounter: report.Ptr(uint64(0)),
	}
	if d.scenario == "pcie-flaky" {
		ic.PCIeGenCurrent = report.Ptr(1)
		ic.PCIeWidthCurrent = report.Ptr(8)
		ic.PCIeReplayCounter = report.Ptr(uint64(4821))
	}
	return ic, nil
}

func (d *device) Limits() (report.Limits, telemetry.Unsupported) {
	return report.Limits{
		PowerLimitMW:   report.Ptr(int64(70000)),
		MaxSMClockMHz:  report.Ptr(int64(1590)),
		MaxMemClockMHz: report.Ptr(int64(5001)),
		SlowdownTempC:  report.Ptr(int64(90)),
		ShutdownTempC:  report.Ptr(int64(96)),
	}, nil
}

func (d *device) Sample() (report.Sample, telemetry.Unsupported) {
	n := int64(d.sampleTick)
	d.sampleTick++

	temp := min(45+2*n, 70)    // warms up, levels off at 70°C
	power := 66000 + (n%3)*400 // ~66 W of the 70 W limit, with a little jitter
	sm := int64(1590)
	throttle := []string{}

	if d.scenario == "thermal-throttle" {
		temp = min(50+3*n, 91)
		if temp >= 85 {
			// Software slowdown first: clocks drop 90 MHz per degree over 84.
			throttle = append(throttle, "sw_thermal_slowdown")
			sm = 1590 - (temp-84)*90
		}
		if temp >= 90 {
			// Hardware slowdown: the GPU protects itself hard.
			throttle = append(throttle, "hw_thermal_slowdown")
			sm = 585
		}
		power = power * sm / 1590 // slower clocks draw less power
	}

	return report.Sample{
		TempC:           report.Ptr(temp),
		PowerMW:         report.Ptr(power),
		SMClockMHz:      report.Ptr(sm),
		MemClockMHz:     report.Ptr(int64(5000)),
		UtilGPUPct:      report.Ptr(int64(100)),
		UtilMemPct:      report.Ptr(int64(62)),
		ThrottleReasons: throttle,
	}, nil
}

// stormXIDs is which XID fires on which poll in the xid-storm scenario.
var stormXIDs = map[int][]uint64{2: {13}, 4: {31}, 5: {31}, 7: {48}, 8: {63}}

func (d *device) PollEvents() ([]report.XIDEvent, telemetry.Unsupported) {
	tick := d.eventTick
	d.eventTick++
	if d.scenario != "xid-storm" {
		return nil, nil
	}
	var out []report.XIDEvent
	for _, x := range stormXIDs[tick] {
		out = append(out, report.XIDEvent{XID: x, Description: telemetry.XIDDescription(x)})
	}
	return out, nil
}
