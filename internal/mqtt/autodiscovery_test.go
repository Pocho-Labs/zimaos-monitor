package mqtt

import (
	"testing"

	"zimaos-monitor/internal/config"
)

func TestUptimeDiscoveryUsesDurationMeasurement(t *testing.T) {
	discovery := uptimeDiscovery("nas", "nas/state", haDevice{}, haOrigin{})
	if discovery.UnitOfMeasurement != "s" {
		t.Fatalf("unit = %q, want s", discovery.UnitOfMeasurement)
	}
	if discovery.DeviceClass != "duration" {
		t.Fatalf("device class = %q, want duration", discovery.DeviceClass)
	}
	if discovery.StateClass != "measurement" {
		t.Fatalf("state class = %q, want measurement", discovery.StateClass)
	}
	if discovery.ValueTemplate != "{{ value_json.uptime_seconds }}" {
		t.Fatalf("unexpected value template %q", discovery.ValueTemplate)
	}
}

func TestMonitorUpdateDiscoveryIsInformational(t *testing.T) {
	cfg := &config.Config{Device: config.DeviceConfig{ID: "nas"}}
	update := monitorUpdateDiscovery(cfg, "nas/monitor/update", haDevice{}, haOrigin{})
	if update.StateTopic != "nas/monitor/update" || update.DeviceClass != "firmware" {
		t.Fatalf("unexpected monitor update discovery: %+v", update)
	}
}
