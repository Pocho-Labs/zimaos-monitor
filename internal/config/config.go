package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type DiskConfig struct {
	Path string `yaml:"path"`
	Name string `yaml:"name"`
}

type MQTTConfig struct {
	Broker   string `yaml:"broker"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	ClientID string `yaml:"client_id"`
}

type DeviceConfig struct {
	Name         string `yaml:"name"`
	ID           string `yaml:"id"`
	Model        string `yaml:"model"`
	Manufacturer string `yaml:"manufacturer"`
	SerialNumber string `yaml:"serial_number"`
	SWVersion    string `yaml:"-"`
}

type UpdatesConfig struct {
	Enabled       *bool         `yaml:"enabled"`
	CheckInterval time.Duration `yaml:"check_interval"`
}

type MonitorUpdatesConfig struct {
	Enabled       *bool         `yaml:"enabled"`
	CheckInterval time.Duration `yaml:"check_interval"`
}

type Config struct {
	MQTT           MQTTConfig           `yaml:"mqtt"`
	Device         DeviceConfig         `yaml:"device"`
	Interval       time.Duration        `yaml:"interval"`
	Disks          []DiskConfig         `yaml:"disks"`
	Updates        UpdatesConfig        `yaml:"updates"`
	MonitorUpdates MonitorUpdatesConfig `yaml:"monitor_updates"`
	Identity       IdentityStatus       `yaml:"-"`
}

func Load(path string) (*Config, error) {
	return LoadWithIdentitySource(path, osIdentitySource{})
}

// LoadWithIdentitySource loads configuration using source for automatic host identities.
// Callers normally use Load; the injected source keeps machine identity behavior testable.
func LoadWithIdentitySource(path string, source IdentitySource) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config %s: %w", path, err)
	}
	defer f.Close()

	var cfg Config
	if err := yaml.NewDecoder(f).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if cfg.Interval == 0 {
		cfg.Interval = 30 * time.Second
	}
	if cfg.Updates.Enabled == nil {
		t := true
		cfg.Updates.Enabled = &t
	}
	if cfg.Updates.CheckInterval == 0 {
		cfg.Updates.CheckInterval = 6 * time.Hour
	}
	if cfg.MonitorUpdates.Enabled == nil {
		t := true
		cfg.MonitorUpdates.Enabled = &t
	}
	if cfg.MonitorUpdates.CheckInterval == 0 {
		cfg.MonitorUpdates.CheckInterval = 6 * time.Hour
	}

	clientAutomatic := cfg.MQTT.ClientID == ""
	deviceAutomatic := cfg.Device.ID == ""
	if clientAutomatic {
		cfg.Identity.ClientIDProvenance = IdentityAutomatic
	} else {
		cfg.Identity.ClientIDProvenance = IdentityExplicit
	}
	if deviceAutomatic {
		cfg.Identity.DeviceIDProvenance = IdentityAutomatic
	} else {
		cfg.Identity.DeviceIDProvenance = IdentityExplicit
	}

	if clientAutomatic || deviceAutomatic {
		machineID, err := canonicalMachineID(source)
		if err != nil {
			return nil, fmt.Errorf("resolve automatic identity: %w", err)
		}
		if clientAutomatic {
			cfg.MQTT.ClientID = deriveClientID(machineID)
			cfg.Identity.Warnings = append(cfg.Identity.Warnings,
				fmt.Sprintf(
					"mqtt.client_id was omitted; using machine-specific automatic ID %q, replacing the shared legacy default on upgrade",
					cfg.MQTT.ClientID,
				),
			)
		}
		if deviceAutomatic {
			cfg.Device.ID = deriveDeviceID(machineID)
			cfg.Identity.Warnings = append(cfg.Identity.Warnings,
				fmt.Sprintf(
					"device.id was omitted; using machine-specific automatic ID %q; a legacy Home Assistant device may require manual cleanup after upgrade",
					cfg.Device.ID,
				),
			)
		}
	}
	if !clientAutomatic && cfg.MQTT.ClientID == "zimaos-monitor" {
		cfg.Identity.Warnings = append(cfg.Identity.Warnings,
			`mqtt.client_id "zimaos-monitor" is a shared legacy value; configure a unique override or omit it for a machine-specific automatic ID`,
		)
	}
	if !deviceAutomatic && !safeDiscoveryDeviceID(cfg.Device.ID) {
		cfg.Identity.Warnings = append(cfg.Identity.Warnings,
			fmt.Sprintf(
				"device.id %q is unsafe for scoped Home Assistant discovery cleanup; cleanup will be skipped until it is changed",
				cfg.Device.ID,
			),
		)
	}

	host := ""
	if source != nil {
		host, _ = source.Hostname()
	}
	host = strings.TrimSpace(host)
	if cfg.Device.Name == "" {
		if host != "" && host != "localhost" {
			cfg.Device.Name = "ZimaOS " + host
		} else {
			cfg.Device.Name = "ZimaOS Monitor"
		}
	}
	if cfg.Device.Manufacturer == "" {
		cfg.Device.Manufacturer = "Pocho Labs"
	}
	if cfg.Device.Model == "" {
		cfg.Device.Model = readDMI("/sys/class/dmi/id/product_name", "/sys/class/dmi/id/board_name")
	}
	if cfg.Device.SerialNumber == "" {
		cfg.Device.SerialNumber = readDMI("/sys/class/dmi/id/product_serial", "/sys/class/dmi/id/board_serial")
	}
	return &cfg, nil
}

func (u UpdatesConfig) IsEnabled() bool {
	return u.Enabled == nil || *u.Enabled
}

func (u MonitorUpdatesConfig) IsEnabled() bool {
	return u.Enabled == nil || *u.Enabled
}

// readDMI reads the first non-generic value from the given DMI sysfs paths.
func readDMI(paths ...string) string {
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		s := strings.TrimSpace(string(b))
		switch s {
		case "", "Default string", "Not Specified", "To Be Filled By O.E.M.", "None", "System Serial Number":
			continue
		}
		return s
	}
	return ""
}
