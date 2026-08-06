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

func TestExplicitIdentitiesRoundTripExactly(t *testing.T) {
	const clientID = "Client ID:Mixed/+ #"
	const deviceID = "Device/ID+Exact #"
	cfg := loadTestConfig(t, `mqtt:
  broker: tcp://localhost:1883
  client_id: "Client ID:Mixed/+ #"
device:
  id: "Device/ID+Exact #"
`)
	if cfg.MQTT.ClientID != clientID {
		t.Fatalf("client ID = %q, want exact %q", cfg.MQTT.ClientID, clientID)
	}
	if cfg.Device.ID != deviceID {
		t.Fatalf("device ID = %q, want exact %q", cfg.Device.ID, deviceID)
	}
}

func TestLoadWithIdentitySource(t *testing.T) {
	const machineID = "0123456789abcdef0123456789abcdef"
	tests := []struct {
		name         string
		config       string
		source       *fakeIdentitySource
		wantClientID string
		wantDeviceID string
		wantErr      bool
		wantReads    int
	}{
		{
			name:   "automatic",
			config: "mqtt:\n  broker: tcp://localhost:1883\n",
			source: &fakeIdentitySource{
				host:  "shared-host",
				files: map[string]string{machineIDPath: machineID},
			},
			wantClientID: deriveClientID(machineID),
			wantDeviceID: deriveDeviceID(machineID),
			wantReads:    1,
		},
		{
			name: "explicit client only",
			config: "mqtt:\n  broker: tcp://localhost:1883\n" +
				"  client_id: exact-client\n",
			source:       &fakeIdentitySource{files: map[string]string{machineIDPath: machineID}},
			wantClientID: "exact-client",
			wantDeviceID: deriveDeviceID(machineID),
			wantReads:    1,
		},
		{
			name: "explicit device only",
			config: "mqtt:\n  broker: tcp://localhost:1883\n" +
				"device:\n  id: Exact/Device\n",
			source:       &fakeIdentitySource{files: map[string]string{machineIDPath: machineID}},
			wantClientID: deriveClientID(machineID),
			wantDeviceID: "Exact/Device",
			wantReads:    1,
		},
		{
			name: "both explicit needs no machine ID",
			config: "mqtt:\n  broker: tcp://localhost:1883\n" +
				"  client_id: exact-client\n" +
				"device:\n  id: Exact/Device\n",
			source:       &fakeIdentitySource{},
			wantClientID: "exact-client",
			wantDeviceID: "Exact/Device",
			wantReads:    0,
		},
		{
			name:      "missing source has no shared fallback",
			config:    "mqtt:\n  broker: tcp://localhost:1883\n",
			source:    &fakeIdentitySource{},
			wantErr:   true,
			wantReads: 2,
		},
		{
			name:      "malformed source",
			config:    "mqtt:\n  broker: tcp://localhost:1883\n",
			source:    &fakeIdentitySource{files: map[string]string{machineIDPath: "bad"}},
			wantErr:   true,
			wantReads: 1,
		},
		{
			name:         "dbus fallback",
			config:       "mqtt:\n  broker: tcp://localhost:1883\n",
			source:       &fakeIdentitySource{files: map[string]string{dbusMachineIDPath: machineID}},
			wantClientID: deriveClientID(machineID),
			wantDeviceID: deriveDeviceID(machineID),
			wantReads:    2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tt.config), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadWithIdentitySource(path, tt.source)
			if (err != nil) != tt.wantErr {
				t.Fatalf("LoadWithIdentitySource() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				if cfg != nil {
					t.Fatalf("config = %#v, want nil on error", cfg)
				}
			} else if cfg.MQTT.ClientID != tt.wantClientID || cfg.Device.ID != tt.wantDeviceID {
				t.Fatalf("identities = %q/%q, want %q/%q",
					cfg.MQTT.ClientID, cfg.Device.ID, tt.wantClientID, tt.wantDeviceID)
			}
			if len(tt.source.reads) != tt.wantReads {
				t.Fatalf("machine ID reads = %d, want %d", len(tt.source.reads), tt.wantReads)
			}
		})
	}
}

func TestLoadWithNilIdentitySourceAllowsFullOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	contents := "mqtt:\n  broker: tcp://localhost:1883\n  client_id: exact-client\n" +
		"device:\n  id: exact-device\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadWithIdentitySource(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MQTT.ClientID != "exact-client" || cfg.Device.ID != "exact-device" {
		t.Fatalf("identities = %q/%q", cfg.MQTT.ClientID, cfg.Device.ID)
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
