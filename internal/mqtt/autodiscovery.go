package mqtt

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"zimaos-monitor/internal/config"
)

const originName = "zimaos-monitor"

type haOrigin struct {
	Name string `json:"name"`
}

type haDevice struct {
	Identifiers  []string `json:"identifiers"`
	Name         string   `json:"name"`
	Model        string   `json:"model,omitempty"`
	Manufacturer string   `json:"manufacturer,omitempty"`
	SerialNumber string   `json:"serial_number,omitempty"`
	SWVersion    string   `json:"sw_version,omitempty"`
}

type haDiscovery struct {
	Name              string   `json:"name"`
	UniqueID          string   `json:"unique_id"`
	StateTopic        string   `json:"state_topic"`
	ValueTemplate     string   `json:"value_template"`
	UnitOfMeasurement string   `json:"unit_of_measurement,omitempty"`
	DeviceClass       string   `json:"device_class,omitempty"`
	StateClass        string   `json:"state_class,omitempty"`
	Icon              string   `json:"icon,omitempty"`
	Device            haDevice `json:"device"`
	Origin            haOrigin `json:"origin"`
}

type haUpdate struct {
	Name        string   `json:"name"`
	Title       string   `json:"title,omitempty"`
	UniqueID    string   `json:"unique_id"`
	StateTopic  string   `json:"state_topic"`
	DeviceClass string   `json:"device_class"`
	Device      haDevice `json:"device"`
	Origin      haOrigin `json:"origin"`
}

// PublishDiscovery publishes Home Assistant MQTT autodiscovery configs for all sensors.
// If purgeStale is true (first call on startup), existing retained discovery topics from
// this publisher that are no longer in the current sensor set are cleared, which prevents
// duplicate entities when disk names or device IDs change between deployments.
func (c *Client) PublishDiscovery(disks []config.DiskConfig, numCores int, purgeStale bool) error {
	model := c.cfg.Device.Model
	if model == "" {
		model = "ZimaOS"
	}
	dev := haDevice{
		Identifiers:  []string{c.cfg.Device.ID},
		Name:         c.cfg.Device.Name,
		Model:        model,
		Manufacturer: c.cfg.Device.Manufacturer,
		SerialNumber: c.cfg.Device.SerialNumber,
		SWVersion:    c.cfg.Device.SWVersion,
	}
	origin := haOrigin{Name: originName}
	stateTopic := fmt.Sprintf("%s/state", c.cfg.Device.ID)
	updateTopic := fmt.Sprintf("%s/update", c.cfg.Device.ID)
	monitorUpdateTopic := fmt.Sprintf("%s/monitor/update", c.cfg.Device.ID)

	sensors := []haDiscovery{
		{
			Name:              "CPU Temperature",
			UniqueID:          fmt.Sprintf("%s_cpu_temp", c.cfg.Device.ID),
			StateTopic:        stateTopic,
			ValueTemplate:     "{{ value_json.cpu_temp | round(1) }}",
			UnitOfMeasurement: "°C",
			DeviceClass:       "temperature",
			StateClass:        "measurement",
			Device:            dev,
			Origin:            origin,
		},
		{
			Name:              "CPU Power",
			UniqueID:          fmt.Sprintf("%s_cpu_watts", c.cfg.Device.ID),
			StateTopic:        stateTopic,
			ValueTemplate:     "{{ value_json.cpu_watts | round(1) }}",
			UnitOfMeasurement: "W",
			DeviceClass:       "power",
			StateClass:        "measurement",
			Device:            dev,
			Origin:            origin,
		},
		{
			Name:              "CPU Usage",
			UniqueID:          fmt.Sprintf("%s_cpu_usage_pct", c.cfg.Device.ID),
			StateTopic:        stateTopic,
			ValueTemplate:     "{{ value_json.cpu_usage_pct | round(1) }}",
			UnitOfMeasurement: "%",
			StateClass:        "measurement",
			Icon:              "mdi:cpu-64-bit",
			Device:            dev,
			Origin:            origin,
		},
		{
			Name:              "RAM Used",
			UniqueID:          fmt.Sprintf("%s_ram_used_pct", c.cfg.Device.ID),
			StateTopic:        stateTopic,
			ValueTemplate:     "{{ value_json.ram_used_pct | round(1) }}",
			UnitOfMeasurement: "%",
			StateClass:        "measurement",
			Icon:              "mdi:memory",
			Device:            dev,
			Origin:            origin,
		},
		{
			Name:              "RAM Available",
			UniqueID:          fmt.Sprintf("%s_ram_available_gb", c.cfg.Device.ID),
			StateTopic:        stateTopic,
			ValueTemplate:     "{{ value_json.ram_available_gb | round(2) }}",
			UnitOfMeasurement: "GB",
			StateClass:        "measurement",
			Icon:              "mdi:memory",
			Device:            dev,
			Origin:            origin,
		},
		{
			Name:              "RAM Total",
			UniqueID:          fmt.Sprintf("%s_ram_total_gb", c.cfg.Device.ID),
			StateTopic:        stateTopic,
			ValueTemplate:     "{{ value_json.ram_total_gb | round(2) }}",
			UnitOfMeasurement: "GB",
			StateClass:        "measurement",
			Icon:              "mdi:memory",
			Device:            dev,
			Origin:            origin,
		},
		{
			Name:          "Load Average 1m",
			UniqueID:      fmt.Sprintf("%s_load_avg_1", c.cfg.Device.ID),
			StateTopic:    stateTopic,
			ValueTemplate: "{{ value_json.load_avg_1 | round(2) }}",
			StateClass:    "measurement",
			Icon:          "mdi:gauge",
			Device:        dev,
			Origin:        origin,
		},
		{
			Name:          "Load Average 5m",
			UniqueID:      fmt.Sprintf("%s_load_avg_5", c.cfg.Device.ID),
			StateTopic:    stateTopic,
			ValueTemplate: "{{ value_json.load_avg_5 | round(2) }}",
			StateClass:    "measurement",
			Icon:          "mdi:gauge",
			Device:        dev,
			Origin:        origin,
		},
		{
			Name:          "Load Average 15m",
			UniqueID:      fmt.Sprintf("%s_load_avg_15", c.cfg.Device.ID),
			StateTopic:    stateTopic,
			ValueTemplate: "{{ value_json.load_avg_15 | round(2) }}",
			StateClass:    "measurement",
			Icon:          "mdi:gauge",
			Device:        dev,
			Origin:        origin,
		},
		uptimeDiscovery(c.cfg.Device.ID, stateTopic, dev, origin),
	}

	for _, d := range disks {
		key := strings.ReplaceAll(d.Name, " ", "_")
		sensors = append(sensors,
			haDiscovery{
				Name:              fmt.Sprintf("%s Used %%", d.Name),
				UniqueID:          fmt.Sprintf("%s_disk_%s_used_pct", c.cfg.Device.ID, key),
				StateTopic:        stateTopic,
				ValueTemplate:     fmt.Sprintf("{{ value_json.disks['%s'].used_pct | round(1) }}", key),
				UnitOfMeasurement: "%",
				StateClass:        "measurement",
				Icon:              "mdi:harddisk",
				Device:            dev,
				Origin:            origin,
			},
			haDiscovery{
				Name:              fmt.Sprintf("%s Used", d.Name),
				UniqueID:          fmt.Sprintf("%s_disk_%s_used_gb", c.cfg.Device.ID, key),
				StateTopic:        stateTopic,
				ValueTemplate:     fmt.Sprintf("{{ value_json.disks['%s'].used_gb | round(2) }}", key),
				UnitOfMeasurement: "GB",
				StateClass:        "measurement",
				Icon:              "mdi:harddisk",
				Device:            dev,
				Origin:            origin,
			},
			haDiscovery{
				Name:              fmt.Sprintf("%s Total", d.Name),
				UniqueID:          fmt.Sprintf("%s_disk_%s_total_gb", c.cfg.Device.ID, key),
				StateTopic:        stateTopic,
				ValueTemplate:     fmt.Sprintf("{{ value_json.disks['%s'].total_gb | round(2) }}", key),
				UnitOfMeasurement: "GB",
				StateClass:        "measurement",
				Icon:              "mdi:harddisk",
				Device:            dev,
				Origin:            origin,
			},
			haDiscovery{
				Name:              fmt.Sprintf("%s Free", d.Name),
				UniqueID:          fmt.Sprintf("%s_disk_%s_free_gb", c.cfg.Device.ID, key),
				StateTopic:        stateTopic,
				ValueTemplate:     fmt.Sprintf("{{ value_json.disks['%s'].free_gb | round(2) }}", key),
				UnitOfMeasurement: "GB",
				StateClass:        "measurement",
				Icon:              "mdi:harddisk",
				Device:            dev,
				Origin:            origin,
			},
		)
	}

	for i := range numCores {
		sensors = append(sensors, haDiscovery{
			Name:              fmt.Sprintf("CPU Core %d Usage", i),
			UniqueID:          fmt.Sprintf("%s_cpu_core_%d_pct", c.cfg.Device.ID, i),
			StateTopic:        stateTopic,
			ValueTemplate:     fmt.Sprintf("{{ value_json.cpu_core_pct[%d] | round(1) }}", i),
			UnitOfMeasurement: "%",
			StateClass:        "measurement",
			Icon:              "mdi:cpu-64-bit",
			Device:            dev,
			Origin:            origin,
		})
	}

	updateEntity := haUpdate{
		Name:        "ZimaOS Version",
		UniqueID:    fmt.Sprintf("%s_zimaos_update", c.cfg.Device.ID),
		StateTopic:  updateTopic,
		DeviceClass: "firmware",
		Device:      dev,
		Origin:      origin,
	}
	monitorUpdateEntity := monitorUpdateDiscovery(c.cfg, monitorUpdateTopic, dev, origin)

	// Build set of desired topics before purging, so we never wipe what we're about to publish.
	desired := make(map[string]bool, len(sensors)+2)
	for _, s := range sensors {
		desired[fmt.Sprintf("homeassistant/sensor/%s/%s/config", c.cfg.Device.ID, s.UniqueID)] = true
	}
	desired[fmt.Sprintf("homeassistant/update/%s/zimaos_version/config", c.cfg.Device.ID)] = true
	if c.cfg.MonitorUpdates.IsEnabled() {
		desired[fmt.Sprintf("homeassistant/update/%s/zimaos_monitor/config", c.cfg.Device.ID)] = true
	}

	if purgeStale {
		c.purgeStaleDiscovery(desired)
	}

	for _, s := range sensors {
		payload, err := json.Marshal(s)
		if err != nil {
			return fmt.Errorf("marshal discovery %s: %w", s.UniqueID, err)
		}
		topic := fmt.Sprintf("homeassistant/sensor/%s/%s/config", c.cfg.Device.ID, s.UniqueID)
		if err := c.Publish(topic, payload, true); err != nil {
			return fmt.Errorf("publish discovery %s: %w", s.UniqueID, err)
		}
	}

	payload, err := json.Marshal(updateEntity)
	if err != nil {
		return fmt.Errorf("marshal update discovery: %w", err)
	}
	topic := fmt.Sprintf("homeassistant/update/%s/zimaos_version/config", c.cfg.Device.ID)
	if err := c.Publish(topic, payload, true); err != nil {
		return fmt.Errorf("publish update discovery: %w", err)
	}

	if c.cfg.MonitorUpdates.IsEnabled() {
		payload, err = json.Marshal(monitorUpdateEntity)
		if err != nil {
			return fmt.Errorf("marshal monitor update discovery: %w", err)
		}
		topic = fmt.Sprintf("homeassistant/update/%s/zimaos_monitor/config", c.cfg.Device.ID)
		if err := c.Publish(topic, payload, true); err != nil {
			return fmt.Errorf("publish monitor update discovery: %w", err)
		}
	}

	return nil
}

func uptimeDiscovery(deviceID, stateTopic string, device haDevice, origin haOrigin) haDiscovery {
	return haDiscovery{
		Name:              "Uptime",
		UniqueID:          fmt.Sprintf("%s_uptime_seconds", deviceID),
		StateTopic:        stateTopic,
		ValueTemplate:     "{{ value_json.uptime_seconds }}",
		UnitOfMeasurement: "s",
		DeviceClass:       "duration",
		StateClass:        "measurement",
		Device:            device,
		Origin:            origin,
	}
}

func monitorUpdateDiscovery(cfg *config.Config, stateTopic string, device haDevice, origin haOrigin) haUpdate {
	update := haUpdate{
		Name:        "zimaos-monitor Update",
		Title:       "zimaos-monitor",
		UniqueID:    fmt.Sprintf("%s_zimaos_monitor_update", cfg.Device.ID),
		StateTopic:  stateTopic,
		DeviceClass: "firmware",
		Device:      device,
		Origin:      origin,
	}
	return update
}

// purgeStaleDiscovery subscribes to all retained homeassistant discovery topics published
// by us (detected via origin.name == "zimaos-monitor"), waits 2s for retained delivery,
// then clears any that are not in `desired`.
func (c *Client) purgeStaleDiscovery(desired map[string]bool) {
	collected, err := c.CollectRetained("homeassistant/+/+/+/config", 2*time.Second)
	if err != nil {
		log.Printf("warn: purge discovery scan: %v", err)
		return
	}

	for topic, payload := range collected {
		if desired[topic] || len(payload) == 0 {
			continue
		}
		var probe struct {
			Origin struct {
				Name string `json:"name"`
			} `json:"origin"`
		}
		if err := json.Unmarshal(payload, &probe); err != nil {
			continue
		}
		if probe.Origin.Name != originName {
			continue
		}
		log.Printf("mqtt: purging stale discovery topic %s", topic)
		if err := c.Publish(topic, nil, true); err != nil {
			log.Printf("warn: purge %s: %v", topic, err)
		}
	}
}
