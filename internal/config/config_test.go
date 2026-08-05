package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
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

func TestGeneratedCredentialRoundTrip(t *testing.T) {
	tests := []struct {
		name     string
		username string
		password string
	}{
		{name: "empty"},
		{name: "username only", username: "monitor"},
		{name: "special characters", username: " mqtt:#'\"\\ü ", password: " p:#'\"\\密碼 "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			document := struct {
				MQTT MQTTConfig `yaml:"mqtt"`
			}{
				MQTT: MQTTConfig{
					Broker:   "tcp://broker.local:1883",
					Username: tt.username,
					Password: tt.password,
				},
			}
			contents, err := yaml.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, contents, 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.MQTT.Username != tt.username || cfg.MQTT.Password != tt.password {
				t.Fatalf("credentials = %q/%q, want %q/%q", cfg.MQTT.Username, cfg.MQTT.Password, tt.username, tt.password)
			}
		})
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
