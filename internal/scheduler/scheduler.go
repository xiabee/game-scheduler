// Package scheduler runs enabled plans on their cron schedules by invoking the
// task service. It supports standard 5-field cron plus the @every / @daily etc.
// descriptors provided by robfig/cron.
package scheduler

import (
	"log/slog"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/xiabee/game-scheduler/internal/store"
	"github.com/xiabee/game-scheduler/internal/task"
)

// Scheduler binds plans to a cron engine.
type Scheduler struct {
	store *store.Store
	svc   *task.Service
	log   *slog.Logger

	mu      sync.Mutex
	cron    *cron.Cron
	entries map[int64]cron.EntryID // planID -> entry
}

// New builds a scheduler. Times use the local timezone.
func New(s *store.Store, svc *task.Service, log *slog.Logger) *Scheduler {
	if log == nil {
		log = slog.Default()
	}
	return &Scheduler{
		store:   s,
		svc:     svc,
		log:     log,
		cron:    cron.New(cron.WithLogger(cron.DiscardLogger)),
		entries: map[int64]cron.EntryID{},
	}
}

// Start loads enabled plans and begins ticking.
func (s *Scheduler) Start() error {
	if err := s.Reload(); err != nil {
		return err
	}
	s.cron.Start()
	s.log.Info("scheduler started", "plans", len(s.entries))
	return nil
}

// Stop halts the cron engine, waiting for running jobs to settle.
func (s *Scheduler) Stop() {
	ctx := s.cron.Stop()
	<-ctx.Done()
}

// Reload re-syncs cron entries with the enabled plans in the database. It is
// safe to call at runtime (e.g. after a plan is created/updated/deleted).
func (s *Scheduler) Reload() error {
	plans, err := s.store.ListPlans(true)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// Drop all current entries and rebuild; plan counts are small.
	for _, id := range s.entries {
		s.cron.Remove(id)
	}
	s.entries = map[int64]cron.EntryID{}

	for _, p := range plans {
		p := p
		id, err := s.cron.AddFunc(p.CronExpr, func() { s.fire(p) })
		if err != nil {
			s.log.Error("invalid cron expr; skipping plan", "plan_id", p.ID, "expr", p.CronExpr, "err", err)
			continue
		}
		s.entries[p.ID] = id
		if entry := s.cron.Entry(id); !entry.Next.IsZero() {
			next := entry.Next
			_ = s.store.SetPlanRunTimes(p.ID, p.LastRunAt, &next)
		}
	}
	return nil
}

func (s *Scheduler) fire(p store.Plan) {
	s.log.Info("plan firing", "plan_id", p.ID, "task_id", p.TaskID, "name", p.Name)
	planID := p.ID
	// Scheduled fires skip if the task is still active, so a long task on a
	// short cron does not stack overlapping runs.
	exec, skipped, err := s.svc.Enqueue(p.TaskID, store.TriggerSchedule, &planID, true)
	if err != nil {
		s.log.Error("plan run failed to start", "plan_id", p.ID, "err", err)
		return
	}
	if skipped {
		s.log.Warn("plan fire skipped; previous run still active", "plan_id", p.ID, "task_id", p.TaskID)
		return
	}
	now := time.Now()
	var next *time.Time
	s.mu.Lock()
	if id, ok := s.entries[p.ID]; ok {
		if e := s.cron.Entry(id); !e.Next.IsZero() {
			n := e.Next
			next = &n
		}
	}
	s.mu.Unlock()
	if err := s.store.SetPlanRunTimes(p.ID, &now, next); err != nil {
		s.log.Warn("update plan run times", "plan_id", p.ID, "err", err)
	}
	s.log.Info("plan started execution", "plan_id", p.ID, "exec_id", exec.ID)
}

// ValidateCron reports whether expr is a valid schedule the scheduler accepts.
func ValidateCron(expr string) error {
	_, err := cron.ParseStandard(expr)
	return err
}
