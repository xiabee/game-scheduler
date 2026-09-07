package config

import (
	"path/filepath"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("GS_ADDR", "")
	cfg, err := Load(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr == "" || cfg.MaxConcurrent < 1 {
		t.Fatalf("bad defaults: %+v", cfg)
	}
}

// Invalid env overrides must be ignored (with a log warning) instead of
// silently or unexpectedly changing behavior.
func TestLoadEnvOverrides(t *testing.T) {
	t.Setenv("GS_MAX_CONCURRENT", "3")
	t.Setenv("GS_OVERLOAD_POLICY", "pause")
	t.Setenv("GS_EXECUTION_RETENTION_DAYS", "7")
	cfg, err := Load(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxConcurrent != 3 || cfg.OverloadPolicy != "pause" || cfg.ExecutionRetentionDays != 7 {
		t.Fatalf("valid overrides not applied: %+v", cfg)
	}
}

func TestLoadEnvInvalidValuesIgnored(t *testing.T) {
	t.Setenv("GS_MAX_CONCURRENT", "abc")
	t.Setenv("GS_OVERLOAD_POLICY", "yolo")
	t.Setenv("GS_MONITOR_ENABLED", "not-a-bool")
	t.Setenv("GS_EXECUTION_RETENTION_DAYS", "-")
	cfg, err := Load(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OverloadPolicy != "alert" {
		t.Errorf("invalid overload policy should keep the default, got %q", cfg.OverloadPolicy)
	}
	if cfg.ExecutionRetentionDays != 30 {
		t.Errorf("invalid retention days should keep the default, got %d", cfg.ExecutionRetentionDays)
	}
	if cfg.MaxConcurrent < 1 {
		t.Errorf("max concurrent should fall back to >=1, got %d", cfg.MaxConcurrent)
	}
}
