package store

import (
	"encoding/json"
	"time"
)

// Execution status values.
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusSuccess   = "success"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

// Execution trigger values.
const (
	TriggerManual   = "manual"
	TriggerSchedule = "schedule"
)

// Game is a configured game plus the external tool used to automate it.
type Game struct {
	ID          string    `json:"id"`           // stable key, e.g. "genshin"
	Name        string    `json:"name"`         // display name
	Adapter     string    `json:"adapter"`      // one of game.Adapter keys
	ToolPath    string    `json:"tool_path"`    // path to the tool executable
	WorkingDir  string    `json:"working_dir"`  // default working dir for the tool
	ExtraConfig string    `json:"extra_config"` // adapter-specific JSON
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ExtraConfigMap parses ExtraConfig as a JSON object. Missing/blank yields an
// empty map.
func (g Game) ExtraConfigMap() (map[string]any, error) {
	return decodeJSONObject(g.ExtraConfig)
}

// Task is one runnable unit for a game (a config group, a route run, a daily
// routine, etc.). Type is interpreted by the game's adapter; Params carries
// adapter-specific arguments as JSON.
type Task struct {
	ID            int64     `json:"id"`
	GameID        string    `json:"game_id"`
	Name          string    `json:"name"`
	Type          string    `json:"type"`
	Params        string    `json:"params"` // adapter-specific JSON object
	MaxRetries    int       `json:"max_retries"`
	RetryDelaySec int       `json:"retry_delay_sec"`
	TimeoutSec    int       `json:"timeout_sec"`
	Enabled       bool      `json:"enabled"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// ParamsMap parses Params as a JSON object.
func (t Task) ParamsMap() (map[string]any, error) {
	return decodeJSONObject(t.Params)
}

// Route is a recorded/predefined path file for a game (e.g. a Fhoe-Rail route
// recording or a BetterGI pathing JSON). Tasks reference routes by file path
// through their Params.
type Route struct {
	ID          int64     `json:"id"`
	GameID      string    `json:"game_id"`
	Name        string    `json:"name"`
	FilePath    string    `json:"file_path"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

// Plan binds a task to a cron schedule.
type Plan struct {
	ID        int64      `json:"id"`
	Name      string     `json:"name"`
	TaskID    int64      `json:"task_id"`
	CronExpr  string     `json:"cron_expr"`
	Enabled   bool       `json:"enabled"`
	LastRunAt *time.Time `json:"last_run_at,omitempty"`
	NextRunAt *time.Time `json:"next_run_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// Execution is one run of a task, manual or scheduled.
type Execution struct {
	ID             int64      `json:"id"`
	TaskID         int64      `json:"task_id"`
	PlanID         *int64     `json:"plan_id,omitempty"`
	Trigger        string     `json:"trigger"`
	Status         string     `json:"status"`
	Command        string     `json:"command"`
	Stdout         string     `json:"stdout"`
	Stderr         string     `json:"stderr"`
	ExitCode       *int       `json:"exit_code,omitempty"`
	ErrorMsg       string     `json:"error_msg"`
	ScreenshotPath string     `json:"screenshot_path"`
	RetryCount     int        `json:"retry_count"`
	StartTime      *time.Time `json:"start_time,omitempty"`
	EndTime        *time.Time `json:"end_time,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

func decodeJSONObject(s string) (map[string]any, error) {
	m := map[string]any{}
	if s == "" {
		return m, nil
	}
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, err
	}
	return m, nil
}
