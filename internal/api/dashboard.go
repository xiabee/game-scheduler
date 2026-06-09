package api

import (
	"net/http"
	"time"

	"github.com/xiabee/game-scheduler/internal/store"
)

// dashboard is the aggregated view powering the control board. It is computed
// from the existing CRUD data in a few queries; small enough to recompute on
// every poll.
type dashboard struct {
	GeneratedAt time.Time     `json:"generated_at"`
	Totals      totals        `json:"totals"`
	Games       []gameSummary `json:"games"`
	Recent      []recentExec  `json:"recent"`
}

type totals struct {
	Games     int `json:"games"`
	Tasks     int `json:"tasks"`
	Plans     int `json:"plans"`
	Running   int `json:"running"`
	Failed24h int `json:"failed_24h"`
}

type taskBrief struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

type planBrief struct {
	ID      int64      `json:"id"`
	Name    string     `json:"name"`
	TaskID  int64      `json:"task_id"`
	Cron    string     `json:"cron"`
	Enabled bool       `json:"enabled"`
	NextRun *time.Time `json:"next_run,omitempty"`
}

type gameSummary struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	Adapter    string      `json:"adapter"`
	Enabled    bool        `json:"enabled"`
	Tasks      int         `json:"tasks"`
	Plans      int         `json:"plans"`
	Active     int         `json:"active"`  // pending+running
	Success    int         `json:"success"` // within recent window
	Failed     int         `json:"failed"`  // within recent window
	Health     string      `json:"health"`  // running|ok|error|warn|idle
	LastStatus string      `json:"last_status,omitempty"`
	LastRun    *time.Time  `json:"last_run,omitempty"`
	LastError  string      `json:"last_error,omitempty"`
	LastExecID int64       `json:"last_exec_id,omitempty"`
	NextRun    *time.Time  `json:"next_run,omitempty"`
	TaskList   []taskBrief `json:"task_list"`
	PlanList   []planBrief `json:"plan_list"`
}

// recentWindow bounds how many executions feed the board's aggregates.
const recentWindow = 500

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	d, err := s.buildDashboard()
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// buildDashboard computes the aggregated view. Shared by the JSON endpoint and
// the SSE stream.
func (s *Server) buildDashboard() (dashboard, error) {
	games, err := s.store.ListGames()
	if err != nil {
		return dashboard{}, err
	}
	tasks, err := s.store.ListTasks("")
	if err != nil {
		return dashboard{}, err
	}
	plans, err := s.store.ListPlans(false)
	if err != nil {
		return dashboard{}, err
	}
	execs, err := s.store.ListExecutions(store.ExecutionFilter{Limit: recentWindow})
	if err != nil {
		return dashboard{}, err
	}

	taskGame := map[int64]string{}   // task id -> game id
	taskName := map[int64]string{}   // task id -> name
	byGame := map[string]*gameSummary{}

	d := dashboard{GeneratedAt: time.Now()}
	for i := range games {
		g := games[i]
		gs := &gameSummary{ID: g.ID, Name: g.Name, Adapter: g.Adapter, Enabled: g.Enabled, TaskList: []taskBrief{}, PlanList: []planBrief{}}
		byGame[g.ID] = gs
	}

	for _, t := range tasks {
		taskGame[t.ID] = t.GameID
		taskName[t.ID] = t.Name
		if gs := byGame[t.GameID]; gs != nil {
			gs.Tasks++
			gs.TaskList = append(gs.TaskList, taskBrief{ID: t.ID, Name: t.Name, Type: t.Type})
		}
	}

	for _, p := range plans {
		gid := taskGame[p.TaskID]
		if gs := byGame[gid]; gs != nil {
			gs.Plans++
			gs.PlanList = append(gs.PlanList, planBrief{
				ID: p.ID, Name: p.Name, TaskID: p.TaskID, Cron: p.CronExpr, Enabled: p.Enabled, NextRun: p.NextRunAt,
			})
			if p.Enabled && p.NextRunAt != nil {
				if gs.NextRun == nil || p.NextRunAt.Before(*gs.NextRun) {
					gs.NextRun = p.NextRunAt
				}
			}
		}
	}

	cutoff := time.Now().Add(-24 * time.Hour)
	for _, e := range execs {
		gid := taskGame[e.TaskID]
		gs := byGame[gid]
		if e.Status == store.StatusRunning {
			d.Totals.Running++
		}
		if e.Status == store.StatusFailed && e.StartTime != nil && e.StartTime.After(cutoff) {
			d.Totals.Failed24h++
		}
		// recent table (cap to first 25, which are the newest)
		if len(d.Recent) < 25 {
			d.Recent = append(d.Recent, recentExec{
				ID:          e.ID,
				GameID:      gid,
				TaskName:    taskName[e.TaskID],
				Status:      e.Status,
				Trigger:     e.Trigger,
				StartTime:   e.StartTime,
				DurationSec: durationSec(e),
				ExitCode:    e.ExitCode,
			})
		}
		if gs == nil {
			continue
		}
		switch e.Status {
		case store.StatusPending, store.StatusRunning:
			gs.Active++
		case store.StatusSuccess:
			gs.Success++
		case store.StatusFailed:
			gs.Failed++
		}
		// first exec encountered per game is the most recent (id desc order)
		if gs.LastExecID == 0 {
			gs.LastStatus = e.Status
			gs.LastRun = e.StartTime
			gs.LastExecID = e.ID
			if e.Status == store.StatusFailed {
				gs.LastError = e.ErrorMsg
			}
		}
	}

	d.Totals.Games = len(games)
	d.Totals.Tasks = len(tasks)
	d.Totals.Plans = len(plans)

	for _, g := range games {
		gs := byGame[g.ID]
		gs.Health = health(gs)
		d.Games = append(d.Games, *gs)
	}

	return d, nil
}

type recentExec struct {
	ID          int64      `json:"id"`
	GameID      string     `json:"game_id"`
	TaskName    string     `json:"task_name"`
	Status      string     `json:"status"`
	Trigger     string     `json:"trigger"`
	StartTime   *time.Time `json:"start_time,omitempty"`
	DurationSec float64    `json:"duration_sec"`
	ExitCode    *int       `json:"exit_code,omitempty"`
}

func durationSec(e store.Execution) float64 {
	if e.StartTime == nil || e.EndTime == nil {
		return 0
	}
	return e.EndTime.Sub(*e.StartTime).Seconds()
}

func health(gs *gameSummary) string {
	switch {
	case gs.Active > 0 && gs.LastStatus == store.StatusRunning:
		return "running"
	case gs.Active > 0:
		return "running"
	case gs.LastStatus == store.StatusFailed:
		return "error"
	case gs.LastStatus == store.StatusCancelled:
		return "warn"
	case gs.LastStatus == store.StatusSuccess:
		return "ok"
	default:
		return "idle"
	}
}
