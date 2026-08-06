package mqtt

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"zimaos-monitor/internal/config"
)

func updateDiscoveryFixture(deviceID, deviceName string) (*config.Config, haDevice, haOrigin) {
	cfg := &config.Config{Device: config.DeviceConfig{ID: deviceID, Name: deviceName}}
	device := haDevice{Identifiers: []string{deviceID}, Name: deviceName}
	return cfg, device, haOrigin{Name: originName}
}

func assertUpdateDiscoveryFields(t *testing.T, got haUpdate, want haUpdate) {
	t.Helper()
	stableGot := got
	stableGot.Name = want.Name
	stableGot.Title = want.Title
	if !reflect.DeepEqual(stableGot, want) {
		t.Fatalf("update discovery = %+v, want %+v", got, want)
	}
	payload, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"command_topic", "payload_install"} {
		if _, ok := fields[forbidden]; ok {
			t.Fatalf("informational update contains %q: %s", forbidden, payload)
		}
	}
}

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

func TestDiscoveryTopicFilter(t *testing.T) {
	got, err := discoveryTopicFilter("device_a")
	if err != nil {
		t.Fatal(err)
	}
	if got != "homeassistant/+/device_a/+/config" {
		t.Fatalf("filter = %q", got)
	}

	for _, deviceID := range []string{"", "device/a", "device+a", "device#a", "device\x00a"} {
		if _, err := discoveryTopicFilter(deviceID); err == nil {
			t.Fatalf("device ID %q should be unsafe for cleanup", deviceID)
		}
	}
}

func TestParseDiscoveryTopic(t *testing.T) {
	tests := []struct {
		topic     string
		component string
		nodeID    string
		objectID  string
		ok        bool
	}{
		{"homeassistant/sensor/device_a/cpu/config", "sensor", "device_a", "cpu", true},
		{"homeassistant/update/device_a/version/config", "update", "device_a", "version", true},
		{"homeassistant/sensor/device_a/config", "", "", "", false},
		{"homeassistant/sensor/device_a/cpu/extra/config", "", "", "", false},
		{"other/sensor/device_a/cpu/config", "", "", "", false},
		{"homeassistant//device_a/cpu/config", "", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.topic, func(t *testing.T) {
			component, nodeID, objectID, ok := parseDiscoveryTopic(tt.topic)
			if component != tt.component || nodeID != tt.nodeID || objectID != tt.objectID || ok != tt.ok {
				t.Fatalf("parse = %q/%q/%q/%v, want %q/%q/%q/%v",
					component, nodeID, objectID, ok,
					tt.component, tt.nodeID, tt.objectID, tt.ok)
			}
		})
	}
}

func TestPlanDiscoveryCleanupIsDeviceLocal(t *testing.T) {
	const (
		deviceA = "device_a"
		deviceB = "device_b"
	)
	aDesired := "homeassistant/sensor/device_a/cpu/config"
	aStale := "homeassistant/sensor/device_a/old/config"
	aStaleUpdate := "homeassistant/update/device_a/old_version/config"
	bStale := "homeassistant/sensor/device_b/old/config"
	wrongOrigin := "homeassistant/sensor/device_a/wrong_origin/config"
	missingIdentifiers := "homeassistant/sensor/device_a/missing_ids/config"
	mismatchedIdentifiers := "homeassistant/sensor/device_a/mismatch/config"
	malformed := "homeassistant/sensor/device_a/malformed/config"

	collected := map[string][]byte{
		aDesired:              discoveryOwnershipPayload(t, originName, deviceA),
		aStale:                discoveryOwnershipPayload(t, originName, deviceA),
		aStaleUpdate:          discoveryOwnershipPayload(t, originName, deviceA),
		bStale:                discoveryOwnershipPayload(t, originName, deviceB),
		wrongOrigin:           discoveryOwnershipPayload(t, "another-publisher", deviceA),
		missingIdentifiers:    discoveryOwnershipPayload(t, originName),
		mismatchedIdentifiers: discoveryOwnershipPayload(t, originName, deviceB),
		malformed:             []byte("{"),
		"invalid/topic":       discoveryOwnershipPayload(t, originName, deviceA),
	}
	plan := planDiscoveryCleanup(collected, map[string]bool{aDesired: true}, deviceA)
	slices.Sort(plan.staleTopics)
	wantStale := []string{aStale, aStaleUpdate}
	slices.Sort(wantStale)
	if !slices.Equal(plan.staleTopics, wantStale) {
		t.Fatalf("stale topics = %#v, want %#v", plan.staleTopics, wantStale)
	}
	if len(plan.ambiguousTopics) != 5 {
		t.Fatalf("ambiguous topics = %#v, want 5", plan.ambiguousTopics)
	}
}

func discoveryOwnershipPayload(t *testing.T, origin string, identifiers ...string) []byte {
	t.Helper()
	payload, err := json.Marshal(struct {
		Origin haOrigin `json:"origin"`
		Device haDevice `json:"device"`
	}{
		Origin: haOrigin{Name: origin},
		Device: haDevice{Identifiers: identifiers},
	})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestMonitorUpdateDiscoveryIsInformational(t *testing.T) {
	cfg := &config.Config{Device: config.DeviceConfig{ID: "nas"}}
	update := monitorUpdateDiscovery(cfg, "nas/monitor/update", haDevice{}, haOrigin{})
	if update.StateTopic != "nas/monitor/update" || update.DeviceClass != "firmware" {
		t.Fatalf("unexpected monitor update discovery: %+v", update)
	}
}

func TestUpdateDiscoveryCompatibilityBaseline(t *testing.T) {
	cfg, device, origin := updateDiscoveryFixture("existing_nas", "Custom NAS")

	zimaOS := zimaOSUpdateDiscovery(cfg, "existing_nas/update", device, origin)
	assertUpdateDiscoveryFields(t, zimaOS, haUpdate{
		Name:        "ZimaOS Version",
		UniqueID:    "existing_nas_zimaos_update",
		StateTopic:  "existing_nas/update",
		DeviceClass: "firmware",
		Device:      device,
		Origin:      origin,
	})

	monitor := monitorUpdateDiscovery(cfg, "existing_nas/monitor/update", device, origin)
	assertUpdateDiscoveryFields(t, monitor, haUpdate{
		Name:        "zimaos-monitor Update",
		Title:       "zimaos-monitor",
		UniqueID:    "existing_nas_zimaos_monitor_update",
		StateTopic:  "existing_nas/monitor/update",
		DeviceClass: "firmware",
		Device:      device,
		Origin:      origin,
	})

	for topic, objectID := range map[string]string{
		"homeassistant/update/existing_nas/zimaos_version/config": "zimaos_version",
		"homeassistant/update/existing_nas/zimaos_monitor/config": "zimaos_monitor",
	} {
		component, nodeID, gotObjectID, ok := parseDiscoveryTopic(topic)
		if !ok || component != "update" || nodeID != "existing_nas" || gotObjectID != objectID {
			t.Fatalf("topic contract changed for %q", topic)
		}
	}
}

func TestUpdateDiscoveriesIdentifySoftwareTarget(t *testing.T) {
	cfg, device, origin := updateDiscoveryFixture("nas", "Storage Room")
	tests := []struct {
		name      string
		discovery haUpdate
		wantName  string
		wantTitle string
	}{
		{
			name:      "zimaos operating system",
			discovery: zimaOSUpdateDiscovery(cfg, "nas/update", device, origin),
			wantName:  "ZimaOS Operating System Update",
			wantTitle: "ZimaOS Operating System",
		},
		{
			name:      "zimaos monitor",
			discovery: monitorUpdateDiscovery(cfg, "nas/monitor/update", device, origin),
			wantName:  "zimaos-monitor Update",
			wantTitle: "zimaos-monitor",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.discovery.Name != tt.wantName || tt.discovery.Title != tt.wantTitle {
				t.Fatalf("presentation = %q/%q, want %q/%q",
					tt.discovery.Name, tt.discovery.Title, tt.wantName, tt.wantTitle)
			}
			if tt.discovery.Device.Name != "Storage Room" {
				t.Fatalf("custom device name changed to %q", tt.discovery.Device.Name)
			}
		})
	}
}

func TestUpdateDiscoveryIdentitySurvivesPresentationRefreshes(t *testing.T) {
	cfg, device, origin := updateDiscoveryFixture("legacy_nas", "Renamed NAS")
	tests := []struct {
		name       string
		build      func() haUpdate
		legacyName string
		wantTitle  string
		uniqueID   string
		stateTopic string
	}{
		{
			name:       "zimaos",
			build:      func() haUpdate { return zimaOSUpdateDiscovery(cfg, "legacy_nas/update", device, origin) },
			legacyName: "ZimaOS Version",
			wantTitle:  ZimaOSUpdateTitle,
			uniqueID:   "legacy_nas_zimaos_update",
			stateTopic: "legacy_nas/update",
		},
		{
			name:       "monitor",
			build:      func() haUpdate { return monitorUpdateDiscovery(cfg, "legacy_nas/monitor/update", device, origin) },
			legacyName: "zimaos-monitor Update",
			wantTitle:  MonitorUpdateTitle,
			uniqueID:   "legacy_nas_zimaos_monitor_update",
			stateTopic: "legacy_nas/monitor/update",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for range 3 {
				got := tt.build()
				legacy := got
				legacy.Name = tt.legacyName
				legacy.Title = ""
				if got.UniqueID != tt.uniqueID || got.StateTopic != tt.stateTopic ||
					got.DeviceClass != "firmware" || got.Title != tt.wantTitle {
					t.Fatalf("refresh changed contract: %+v", got)
				}
				assertUpdateDiscoveryFields(t, got, legacy)
			}
		})
	}
}

func TestExplicitDeviceDiscoveryContract(t *testing.T) {
	const deviceID = "existing_nas"
	device := haDevice{Identifiers: []string{deviceID}}
	origin := haOrigin{Name: originName}

	uptime := uptimeDiscovery(deviceID, deviceID+"/state", device, origin)
	if uptime.UniqueID != deviceID+"_uptime_seconds" {
		t.Fatalf("uptime unique ID = %q", uptime.UniqueID)
	}
	if uptime.StateTopic != deviceID+"/state" {
		t.Fatalf("uptime state topic = %q", uptime.StateTopic)
	}
	if len(uptime.Device.Identifiers) != 1 || uptime.Device.Identifiers[0] != deviceID {
		t.Fatalf("uptime device identifiers = %#v", uptime.Device.Identifiers)
	}

	cfg := &config.Config{Device: config.DeviceConfig{ID: deviceID}}
	update := monitorUpdateDiscovery(cfg, deviceID+"/monitor/update", device, origin)
	if update.UniqueID != deviceID+"_zimaos_monitor_update" {
		t.Fatalf("update unique ID = %q", update.UniqueID)
	}
	if update.StateTopic != deviceID+"/monitor/update" {
		t.Fatalf("update state topic = %q", update.StateTopic)
	}
	if len(update.Device.Identifiers) != 1 || update.Device.Identifiers[0] != deviceID {
		t.Fatalf("update device identifiers = %#v", update.Device.Identifiers)
	}
}
