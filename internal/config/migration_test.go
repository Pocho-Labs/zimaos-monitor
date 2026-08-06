package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAutomaticIdentityMigrationIsReadOnlyAndRedacted(t *testing.T) {
	const machineID = "0123456789abcdef0123456789abcdef"
	const password = "top-secret-password"
	contents := []byte("mqtt:\n  broker: tcp://localhost:1883\n  password: " + password + "\n")
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	source := &fakeIdentitySource{
		host:  "legacy-host",
		files: map[string]string{machineIDPath: machineID},
	}
	cfg, err := LoadWithIdentitySource(path, source)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Identity.ClientIDProvenance != IdentityAutomatic ||
		cfg.Identity.DeviceIDProvenance != IdentityAutomatic {
		t.Fatalf("provenance = %#v, want both automatic", cfg.Identity)
	}
	if len(cfg.Identity.Warnings) != 2 {
		t.Fatalf("warnings = %#v, want two automatic migration notices", cfg.Identity.Warnings)
	}
	for _, warning := range cfg.Identity.Warnings {
		if strings.Contains(warning, machineID) || strings.Contains(warning, password) {
			t.Fatalf("warning leaked private input: %q", warning)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(contents) {
		t.Fatalf("configuration changed during load:\n%s", after)
	}
}

func TestExplicitIdentityWarningsAndProvenance(t *testing.T) {
	tests := []struct {
		name        string
		clientID    string
		deviceID    string
		wantWarning string
	}{
		{
			name:        "legacy shared client",
			clientID:    "zimaos-monitor",
			deviceID:    "safe_device",
			wantWarning: "shared",
		},
		{
			name:        "unsafe cleanup device",
			clientID:    "unique-client",
			deviceID:    "unsafe/device",
			wantWarning: "cleanup",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			contents := "mqtt:\n  broker: tcp://localhost:1883\n  client_id: " + tt.clientID +
				"\ndevice:\n  id: " + tt.deviceID + "\n"
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			source := &fakeIdentitySource{}
			cfg, err := LoadWithIdentitySource(path, source)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Identity.ClientIDProvenance != IdentityExplicit ||
				cfg.Identity.DeviceIDProvenance != IdentityExplicit {
				t.Fatalf("provenance = %#v, want both explicit", cfg.Identity)
			}
			if cfg.MQTT.ClientID != tt.clientID || cfg.Device.ID != tt.deviceID {
				t.Fatalf("explicit values changed to %q/%q", cfg.MQTT.ClientID, cfg.Device.ID)
			}
			if len(cfg.Identity.Warnings) != 1 ||
				!strings.Contains(strings.ToLower(cfg.Identity.Warnings[0]), tt.wantWarning) {
				t.Fatalf("warnings = %#v, want one containing %q", cfg.Identity.Warnings, tt.wantWarning)
			}
		})
	}
}
