package monitor

import (
	"testing"

	"github.com/xiabee/game-scheduler/internal/events"
)

func newMon(policy string) *Monitor {
	return New(Config{
		Enabled: true, CPUThreshold: 90, MemThreshold: 90, Policy: policy,
	}, func() (Reading, error) { return Reading{}, nil }, events.New(), nil)
}

func TestOverloadHysteresis(t *testing.T) {
	m := newMon(PolicyAlert)

	// One over-threshold sample is not enough (needs breachesToTrip=2).
	m.update(Reading{CPUPercent: 95, MemPercent: 50})
	if m.Overloaded() {
		t.Fatal("should not trip after a single breach")
	}
	// Second consecutive breach trips it.
	m.update(Reading{CPUPercent: 96, MemPercent: 50})
	if !m.Overloaded() {
		t.Fatal("should trip after two consecutive breaches")
	}
	if r := m.Current().Reason; r == "" {
		t.Error("expected an overload reason")
	}
	// A sample under threshold clears it immediately.
	m.update(Reading{CPUPercent: 10, MemPercent: 50})
	if m.Overloaded() {
		t.Fatal("should clear once usage drops")
	}
	if m.Current().Reason != "" {
		t.Error("reason should clear")
	}
}

func TestMemoryTrips(t *testing.T) {
	m := newMon(PolicyAlert)
	m.update(Reading{CPUPercent: 5, MemPercent: 92})
	m.update(Reading{CPUPercent: 5, MemPercent: 93})
	if !m.Overloaded() {
		t.Fatal("memory over threshold should trip overload")
	}
}

func TestShouldPausePolicy(t *testing.T) {
	// alert policy never pauses even when overloaded
	alert := newMon(PolicyAlert)
	alert.update(Reading{CPUPercent: 99, MemPercent: 10})
	alert.update(Reading{CPUPercent: 99, MemPercent: 10})
	if !alert.Overloaded() || alert.ShouldPause() {
		t.Errorf("alert policy: overloaded=%v shouldPause=%v (want true/false)", alert.Overloaded(), alert.ShouldPause())
	}

	// pause policy pauses while overloaded
	pause := newMon(PolicyPause)
	if pause.ShouldPause() {
		t.Error("should not pause when not overloaded")
	}
	pause.update(Reading{CPUPercent: 99, MemPercent: 10})
	pause.update(Reading{CPUPercent: 99, MemPercent: 10})
	if !pause.ShouldPause() {
		t.Error("pause policy should pause while overloaded")
	}
}

func TestDisabledMonitorNeverPauses(t *testing.T) {
	m := New(Config{Enabled: false, Policy: PolicyPause, CPUThreshold: 90}, func() (Reading, error) { return Reading{}, nil }, events.New(), nil)
	m.update(Reading{CPUPercent: 99})
	m.update(Reading{CPUPercent: 99})
	if m.ShouldPause() {
		t.Error("disabled monitor must never pause")
	}
}

func TestHistoryAndDisk(t *testing.T) {
	m := newMon(PolicyAlert)
	for i := 0; i < historyLen+10; i++ {
		m.update(Reading{CPUPercent: float64(i % 100), MemPercent: 40, DiskPercent: 55, DiskUsedMB: 100, DiskTotalMB: 200})
	}
	s := m.Current()
	if len(s.CPUHistory) != historyLen {
		t.Errorf("cpu history len=%d want %d (capped)", len(s.CPUHistory), historyLen)
	}
	if len(s.MemHistory) != historyLen || len(s.DiskHistory) != historyLen {
		t.Errorf("mem/disk history not tracked: %d/%d", len(s.MemHistory), len(s.DiskHistory))
	}
	if s.DiskPercent != 55 || s.DiskTotalMB != 200 {
		t.Errorf("disk not recorded: %+v", s)
	}
	// Current returns copies — mutating them must not affect the monitor.
	s.CPUHistory[0] = -1
	if m.Current().CPUHistory[0] == -1 {
		t.Error("Current() must return a copy of history")
	}
}

func TestThresholdDisabled(t *testing.T) {
	// CPUThreshold<=0 disables the CPU dimension.
	m := New(Config{Enabled: true, CPUThreshold: 0, MemThreshold: 90}, func() (Reading, error) { return Reading{}, nil }, events.New(), nil)
	m.update(Reading{CPUPercent: 100, MemPercent: 10})
	m.update(Reading{CPUPercent: 100, MemPercent: 10})
	if m.Overloaded() {
		t.Error("CPUThreshold<=0 should not trip on CPU")
	}
}
