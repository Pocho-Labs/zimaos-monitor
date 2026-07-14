package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMonitorUpdatesDefaults(t *testing.T) {
	cfg := loadTestConfig(t, "mqtt:\n  broker: tcp://localhost:1883\n")
	if !cfg.MonitorUpdates.IsEnabled() {
		t.Fatal("monitor update checks should be enabled by default")
	}
	if cfg.MonitorUpdates.CheckInterval != 6*time.Hour {
		t.Fatalf("check interval = %v, want 6h", cfg.MonitorUpdates.CheckInterval)
	}
}

func TestMonitorUpdatesOverrides(t *testing.T) {
	cfg := loadTestConfig(t, `mqtt:
  broker: tcp://localhost:1883
monitor_updates:
  enabled: false
  check_interval: 2h
`)
	if cfg.MonitorUpdates.IsEnabled() {
		t.Fatal("monitor update checks should be disabled")
	}
	if cfg.MonitorUpdates.CheckInterval != 2*time.Hour {
		t.Fatalf("check interval = %v, want 2h", cfg.MonitorUpdates.CheckInterval)
	}
}

func loadTestConfig(t *testing.T, contents string) *Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}
