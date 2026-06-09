// Package monitor samples local machine resources (CPU and memory) on an
// interval and tracks an "overload" state, so the dashboard can show live
// usage and the scheduler can avoid launching new automation while the machine
// is saturated. It is read-only observability plus a scheduling gate — it never
// touches the games or the tools.
package monitor

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"

	"github.com/xiabee/game-scheduler/internal/events"
)

// Reading is a single resource sample.
type Reading struct {
	CPUPercent  float64
	MemPercent  float64
	MemUsedMB   uint64
	MemTotalMB  uint64
	DiskPercent float64
	DiskUsedMB  uint64
	DiskTotalMB uint64
}

// historyLen is how many recent samples are kept for the dashboard sparklines.
const historyLen = 60

// Sampler returns one reading. Pluggable so tests can drive the overload logic
// without depending on real machine load.
type Sampler func() (Reading, error)

// Snapshot is the monitor's current state, surfaced to the dashboard as JSON.
type Snapshot struct {
	Enabled      bool      `json:"enabled"`
	CPUPercent   float64   `json:"cpu_percent"`
	MemPercent   float64   `json:"mem_percent"`
	MemUsedMB    uint64    `json:"mem_used_mb"`
	MemTotalMB   uint64    `json:"mem_total_mb"`
	DiskPercent  float64   `json:"disk_percent"`
	DiskUsedMB   uint64    `json:"disk_used_mb"`
	DiskTotalMB  uint64    `json:"disk_total_mb"`
	CPUThreshold float64   `json:"cpu_threshold"`
	MemThreshold float64   `json:"mem_threshold"`
	Overloaded   bool      `json:"overloaded"`
	Reason       string    `json:"reason,omitempty"`
	Policy       string    `json:"policy"`
	SampledAt    time.Time `json:"sampled_at"`
	// Rolling history (oldest→newest) for sparklines; disk is informational.
	CPUHistory  []float64 `json:"cpu_history"`
	MemHistory  []float64 `json:"mem_history"`
	DiskHistory []float64 `json:"disk_history"`
}

// Policy values.
const (
	PolicyAlert = "alert" // surface overload only
	PolicyPause = "pause" // also skip new scheduled runs while overloaded
)

// Config controls the monitor.
type Config struct {
	Enabled      bool
	CPUThreshold float64       // percent (0-100); <=0 disables the CPU trip
	MemThreshold float64       // percent (0-100); <=0 disables the mem trip
	Interval     time.Duration // sampling period
	Policy       string        // PolicyAlert | PolicyPause
	DiskPath     string        // filesystem to report disk usage for (informational)
}

// breachesToTrip requires this many consecutive over-threshold samples before
// declaring overload, to avoid flapping on momentary spikes.
const breachesToTrip = 2

// Monitor samples resources and exposes the current snapshot.
type Monitor struct {
	cfg     Config
	sampler Sampler
	bus     *events.Bus
	log     *slog.Logger

	notify func(event, title, message string) // optional operator alert hook

	mu       sync.RWMutex
	snap     Snapshot
	breach   int
	cpuHist  []float64
	memHist  []float64
	diskHist []float64
}

// SetNotify installs an operator-alert hook, called when overload trips.
func (m *Monitor) SetNotify(fn func(event, title, message string)) { m.notify = fn }

// New builds a monitor. sampler defaults to the gopsutil-backed sampler; bus
// and log may be nil.
func New(cfg Config, sampler Sampler, bus *events.Bus, log *slog.Logger) *Monitor {
	if sampler == nil {
		sampler = newGopsutilSampler(cfg.DiskPath)
	}
	if log == nil {
		log = slog.Default()
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 3 * time.Second
	}
	if cfg.Policy == "" {
		cfg.Policy = PolicyAlert
	}
	return &Monitor{
		cfg:     cfg,
		sampler: sampler,
		bus:     bus,
		log:     log,
		snap: Snapshot{
			Enabled:      cfg.Enabled,
			CPUThreshold: cfg.CPUThreshold,
			MemThreshold: cfg.MemThreshold,
			Policy:       cfg.Policy,
		},
	}
}

// Start begins sampling until ctx is cancelled. No-op if disabled.
func (m *Monitor) Start(ctx context.Context) {
	if !m.cfg.Enabled {
		return
	}
	go m.loop(ctx)
}

func (m *Monitor) loop(ctx context.Context) {
	// Prime: the first CPU reading covers since-boot and is meaningless as a
	// rate, so discard it before the ticker takes over.
	_, _ = m.sampler()
	t := time.NewTicker(m.cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r, err := m.sampler()
			if err != nil {
				m.log.Warn("resource sample failed", "err", err)
				continue
			}
			m.update(r)
		}
	}
}

// update records a reading and recomputes the overload state with hysteresis.
func (m *Monitor) update(r Reading) {
	overCPU := m.cfg.CPUThreshold > 0 && r.CPUPercent >= m.cfg.CPUThreshold
	overMem := m.cfg.MemThreshold > 0 && r.MemPercent >= m.cfg.MemThreshold
	over := overCPU || overMem
	reason := ""
	if overCPU {
		reason = fmt.Sprintf("CPU %.0f%% ≥ %.0f%%", r.CPUPercent, m.cfg.CPUThreshold)
	}
	if overMem {
		if reason != "" {
			reason += "；"
		}
		reason += fmt.Sprintf("内存 %.0f%% ≥ %.0f%%", r.MemPercent, m.cfg.MemThreshold)
	}

	m.mu.Lock()
	if over {
		m.breach++
	} else {
		m.breach = 0
	}
	was := m.snap.Overloaded
	m.snap.CPUPercent = r.CPUPercent
	m.snap.MemPercent = r.MemPercent
	m.snap.MemUsedMB = r.MemUsedMB
	m.snap.MemTotalMB = r.MemTotalMB
	m.snap.DiskPercent = r.DiskPercent
	m.snap.DiskUsedMB = r.DiskUsedMB
	m.snap.DiskTotalMB = r.DiskTotalMB
	m.snap.SampledAt = time.Now()
	m.cpuHist = appendHist(m.cpuHist, r.CPUPercent)
	m.memHist = appendHist(m.memHist, r.MemPercent)
	m.diskHist = appendHist(m.diskHist, r.DiskPercent)
	if m.breach >= breachesToTrip {
		m.snap.Overloaded = true
		m.snap.Reason = reason
	} else if !over {
		m.snap.Overloaded = false
		m.snap.Reason = ""
	}
	now := m.snap.Overloaded
	m.mu.Unlock()

	if now && !was {
		m.log.Warn("resource overload", "reason", reason)
		if m.notify != nil {
			m.notify("overload", "资源过载", reason)
		}
	} else if !now && was {
		m.log.Info("resource overload cleared")
	}
	m.bus.Notify()
}

// appendHist appends v, keeping at most historyLen most-recent values.
func appendHist(h []float64, v float64) []float64 {
	h = append(h, v)
	if len(h) > historyLen {
		h = h[len(h)-historyLen:]
	}
	return h
}

// Current returns a copy of the latest snapshot, including copies of the
// history slices so callers can read them without racing the sampler.
func (m *Monitor) Current() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s := m.snap
	s.CPUHistory = append([]float64(nil), m.cpuHist...)
	s.MemHistory = append([]float64(nil), m.memHist...)
	s.DiskHistory = append([]float64(nil), m.diskHist...)
	return s
}

// Overloaded reports whether the machine is currently overloaded.
func (m *Monitor) Overloaded() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.snap.Overloaded
}

// ShouldPause reports whether new scheduled runs should be held back: enabled,
// policy is "pause", and the machine is overloaded.
func (m *Monitor) ShouldPause() bool {
	if m == nil || !m.cfg.Enabled || m.cfg.Policy != PolicyPause {
		return false
	}
	return m.Overloaded()
}

// newGopsutilSampler reads real CPU, memory and disk usage. cpu.Percent(0,...)
// returns usage since the previous call, so the priming call in loop() matters.
// diskPath selects the filesystem to report; empty falls back to the current
// directory.
func newGopsutilSampler(diskPath string) Sampler {
	if diskPath == "" {
		diskPath = "."
	}
	return func() (Reading, error) {
		pcts, err := cpu.Percent(0, false)
		if err != nil {
			return Reading{}, err
		}
		var c float64
		if len(pcts) > 0 {
			c = pcts[0]
		}
		vm, err := mem.VirtualMemory()
		if err != nil {
			return Reading{}, err
		}
		r := Reading{
			CPUPercent: c,
			MemPercent: vm.UsedPercent,
			MemUsedMB:  vm.Used / (1024 * 1024),
			MemTotalMB: vm.Total / (1024 * 1024),
		}
		// Disk is best-effort/informational: don't fail a sample if it errors.
		if du, err := disk.Usage(diskPath); err == nil {
			r.DiskPercent = du.UsedPercent
			r.DiskUsedMB = du.Used / (1024 * 1024)
			r.DiskTotalMB = du.Total / (1024 * 1024)
		}
		return r, nil
	}
}
