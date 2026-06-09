// Package config loads server-level configuration. Per-game tool paths are
// stored in the database (see internal/store), not here. This file only holds
// process-wide settings for the server and CLI.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config holds server-wide settings.
type Config struct {
	// Addr is the HTTP listen address for the REST API.
	Addr string `json:"addr"`
	// DataDir is the base directory for the SQLite DB, logs and screenshots.
	DataDir string `json:"data_dir"`
	// DBPath is the SQLite file path. Defaults to <DataDir>/scheduler.db.
	DBPath string `json:"db_path"`
	// ScreenshotCmd, if set, is run when a task fails so an operator can see
	// the game state at the moment of failure. It is a template where
	// {{.Path}} is replaced with the destination PNG path. Example on Windows:
	//   "powershell -NoProfile -Command \"Add-Type -AssemblyName System.Windows.Forms,System.Drawing; $b=[System.Windows.Forms.SystemInformation]::VirtualScreen; $bmp=New-Object System.Drawing.Bitmap($b.Width,$b.Height); $g=[System.Drawing.Graphics]::FromImage($bmp); $g.CopyFromScreen($b.Location,[System.Drawing.Point]::Empty,$b.Size); $bmp.Save('{{.Path}}')\""
	// It is purely an observability helper; it never touches the game process.
	ScreenshotCmd string `json:"screenshot_cmd"`
	// MaxConcurrent is how many executions may run at once. The supported tools
	// drive the shared mouse/keyboard and foreground window, so the safe (and
	// default) value is 1 — fully serialized. Raise it only if your executions
	// genuinely target independent machines/VMs.
	MaxConcurrent int `json:"max_concurrent"`
	// AuthToken, if non-empty, protects the API: every /api/* and /screenshots/*
	// request must present it via `Authorization: Bearer <token>` or a `?token=`
	// query parameter. The dashboard page and /healthz stay open. Empty means no
	// auth (safe only when bound to localhost). Set GS_AUTH_TOKEN to enable.
	AuthToken string `json:"auth_token"`

	// --- resource monitor ---
	// MonitorEnabled turns on live CPU/RAM sampling (default true).
	MonitorEnabled bool `json:"monitor_enabled"`
	// CPUThreshold / MemThreshold are the overload trip points in percent
	// (defaults 90). A value <=0 disables that dimension.
	CPUThreshold float64 `json:"cpu_threshold"`
	MemThreshold float64 `json:"mem_threshold"`
	// MonitorIntervalSec is the sampling period in seconds (default 3).
	MonitorIntervalSec int `json:"monitor_interval_sec"`
	// OverloadPolicy is "alert" (default, surface only) or "pause" (also skip
	// new scheduled runs while overloaded).
	OverloadPolicy string `json:"overload_policy"`
}

// Default returns a Config with sensible defaults. DBPath is intentionally
// left blank so Load can derive it from the final (possibly overridden)
// DataDir; an explicit db_path in the config file or GS_DB_PATH still wins.
func Default() Config {
	return Config{
		Addr:               "127.0.0.1:8080",
		DataDir:            "data",
		MaxConcurrent:      1,
		MonitorEnabled:     true,
		CPUThreshold:       90,
		MemThreshold:       90,
		MonitorIntervalSec: 3,
		OverloadPolicy:     "alert",
	}
}

// Load reads a JSON config file (if path is non-empty and exists), then
// applies environment overrides (GS_ADDR, GS_DATA_DIR, GS_DB_PATH,
// GS_SCREENSHOT_CMD), filling any unset field with defaults.
func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return cfg, err
		}
		if err == nil {
			if err := json.Unmarshal(b, &cfg); err != nil {
				return cfg, err
			}
		}
	}
	if v := os.Getenv("GS_ADDR"); v != "" {
		cfg.Addr = v
	}
	if v := os.Getenv("GS_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
	if v := os.Getenv("GS_DB_PATH"); v != "" {
		cfg.DBPath = v
	}
	if v := os.Getenv("GS_SCREENSHOT_CMD"); v != "" {
		cfg.ScreenshotCmd = v
	}
	if v := os.Getenv("GS_MAX_CONCURRENT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.MaxConcurrent = n
		}
	}
	if v := os.Getenv("GS_AUTH_TOKEN"); v != "" {
		cfg.AuthToken = v
	}
	if v := os.Getenv("GS_MONITOR_ENABLED"); v != "" {
		cfg.MonitorEnabled, _ = strconv.ParseBool(v)
	}
	if v := os.Getenv("GS_CPU_THRESHOLD"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.CPUThreshold = f
		}
	}
	if v := os.Getenv("GS_MEM_THRESHOLD"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.MemThreshold = f
		}
	}
	if v := os.Getenv("GS_MONITOR_INTERVAL_SEC"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.MonitorIntervalSec = n
		}
	}
	if v := os.Getenv("GS_OVERLOAD_POLICY"); v != "" {
		cfg.OverloadPolicy = v
	}
	if cfg.MaxConcurrent < 1 {
		cfg.MaxConcurrent = 1
	}
	if strings.TrimSpace(cfg.DataDir) == "" {
		cfg.DataDir = "data"
	}
	if strings.TrimSpace(cfg.DBPath) == "" {
		cfg.DBPath = filepath.Join(cfg.DataDir, "scheduler.db")
	}
	return cfg, nil
}

// EnsureDirs creates the data directory and screenshot subdirectory.
func (c Config) EnsureDirs() error {
	if err := os.MkdirAll(c.DataDir, 0o755); err != nil {
		return err
	}
	return os.MkdirAll(c.ScreenshotDir(), 0o755)
}

// ScreenshotDir is where failure screenshots are written.
func (c Config) ScreenshotDir() string {
	return filepath.Join(c.DataDir, "screenshots")
}
