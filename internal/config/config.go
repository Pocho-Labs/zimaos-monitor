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
}

func Load(path string) (*Config, error) {
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
	if cfg.MQTT.ClientID == "" {
		cfg.MQTT.ClientID = "zimaos-monitor"
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

	host := hostname()

	if cfg.Device.ID == "" {
		machineID := readMachineID()
		cfg.Device.ID = deriveDeviceID(host, machineID)
	}
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

func hostname() string {
	h, _ := os.Hostname()
	return strings.TrimSpace(h)
}

func readMachineID() string {
	b, err := os.ReadFile("/etc/machine-id")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func deriveDeviceID(host, machineID string) string {
	if host != "" && host != "localhost" {
		return sanitizeID(host)
	}
	if len(machineID) >= 8 {
		return "zimaos_" + machineID[:8]
	}
	return "zimaos_monitor"
}

func sanitizeID(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}
