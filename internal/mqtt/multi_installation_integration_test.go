package mqtt

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
	"zimaos-monitor/internal/config"
)

func TestTwoSimultaneousInstallationsUpdatePresentation(t *testing.T) {
	broker := os.Getenv("MQTT_TEST_BROKER")
	if broker == "" {
		t.Skip("MQTT_TEST_BROKER is not set")
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	enabled := true
	deviceID := "updates_" + suffix
	cfg := &config.Config{
		MQTT: config.MQTTConfig{
			Broker:   broker,
			ClientID: "updates-client-" + suffix,
		},
		Device: config.DeviceConfig{ID: deviceID, Name: "Update Test NAS"},
		MonitorUpdates: config.MonitorUpdatesConfig{
			Enabled: &enabled,
		},
	}

	client, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := client.PublishDiscovery(nil, 0, false); err != nil {
			t.Fatal(err)
		}
	}
	client.Disconnect()

	client, err = NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect()
	if err := client.PublishDiscovery(nil, 0, false); err != nil {
		t.Fatal(err)
	}

	zimaOSTopic := fmt.Sprintf("homeassistant/update/%s/zimaos_version/config", deviceID)
	monitorTopic := fmt.Sprintf("homeassistant/update/%s/zimaos_monitor/config", deviceID)
	allRetained, err := client.CollectRetained("homeassistant/+/"+deviceID+"/+/config", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for topic := range allRetained {
			_ = client.Publish(topic, nil, true)
		}
	}()

	retained := make(map[string][]byte)
	for topic, payload := range allRetained {
		component, _, _, ok := parseDiscoveryTopic(topic)
		if ok && component == "update" {
			retained[topic] = payload
		}
	}
	if len(retained) != 2 {
		t.Fatalf("retained update topics = %#v, want exactly 2", retained)
	}
	for topic, want := range map[string]haUpdate{
		zimaOSTopic: {
			Title:    ZimaOSUpdateTitle,
			UniqueID: deviceID + "_zimaos_update",
		},
		monitorTopic: {
			Title:    MonitorUpdateTitle,
			UniqueID: deviceID + "_zimaos_monitor_update",
		},
	} {
		var got haUpdate
		if err := json.Unmarshal(retained[topic], &got); err != nil {
			t.Fatalf("decode %s: %v", topic, err)
		}
		if got.Title != want.Title || got.UniqueID != want.UniqueID {
			t.Fatalf("retained %s = title %q unique_id %q", topic, got.Title, got.UniqueID)
		}
	}
}

type integrationIdentitySource struct {
	hostname  string
	machineID string
}

func (s integrationIdentitySource) Hostname() (string, error) {
	return s.hostname, nil
}

func (s integrationIdentitySource) ReadFile(path string) ([]byte, error) {
	if path == "/etc/machine-id" {
		return []byte(s.machineID), nil
	}
	return nil, os.ErrNotExist
}

func TestTwoSimultaneousInstallations(t *testing.T) {
	broker := os.Getenv("MQTT_TEST_BROKER")
	if broker == "" {
		t.Skip("MQTT_TEST_BROKER is not set")
	}

	cfgA := loadIntegrationConfig(t, broker, integrationIdentitySource{
		hostname:  "shared-host",
		machineID: "0123456789abcdef0123456789abcdef",
	})
	cfgB := loadIntegrationConfig(t, broker, integrationIdentitySource{
		hostname:  "shared-host",
		machineID: "fedcba9876543210fedcba9876543210",
	})
	if cfgA.MQTT.ClientID == cfgB.MQTT.ClientID || cfgA.Device.ID == cfgB.Device.ID {
		t.Fatalf("automatic identities collided: A=%q/%q B=%q/%q",
			cfgA.MQTT.ClientID, cfgA.Device.ID, cfgB.MQTT.ClientID, cfgB.Device.ID)
	}

	observer := newIntegrationObserver(t, broker)
	defer observer.Disconnect(100)
	received := subscribeIntegrationTopics(t, observer,
		cfgA.Device.ID+"/state",
		cfgB.Device.ID+"/state",
	)

	clientA, err := NewClient(cfgA)
	if err != nil {
		t.Fatal(err)
	}
	defer clientA.Disconnect()
	clientB, err := NewClient(cfgB)
	if err != nil {
		t.Fatal(err)
	}
	defer clientB.Disconnect()

	publishAndAwait(t, clientA, cfgA.Device.ID+"/state", []byte("from-a"), received)
	publishAndAwait(t, clientB, cfgB.Device.ID+"/state", []byte("from-b"), received)

	clientA.Disconnect()
	clientA, err = NewClient(cfgA)
	if err != nil {
		t.Fatal(err)
	}
	defer clientA.Disconnect()

	publishAndAwait(t, clientB, cfgB.Device.ID+"/state", []byte("b-still-online"), received)
	publishAndAwait(t, clientA, cfgA.Device.ID+"/state", []byte("a-reconnected"), received)
}

func TestScopedDiscoveryCleanup(t *testing.T) {
	broker := os.Getenv("MQTT_TEST_BROKER")
	if broker == "" {
		t.Skip("MQTT_TEST_BROKER is not set")
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	deviceA := "cleanup_a_" + suffix
	deviceB := "cleanup_b_" + suffix
	cfgA := &config.Config{
		MQTT:   config.MQTTConfig{Broker: broker, ClientID: "cleanup-client-a-" + suffix},
		Device: config.DeviceConfig{ID: deviceA, Name: deviceA},
	}
	cfgB := &config.Config{
		MQTT:   config.MQTTConfig{Broker: broker, ClientID: "cleanup-client-b-" + suffix},
		Device: config.DeviceConfig{ID: deviceB, Name: deviceB},
	}
	clientA, err := NewClient(cfgA)
	if err != nil {
		t.Fatal(err)
	}
	defer clientA.Disconnect()
	clientB, err := NewClient(cfgB)
	if err != nil {
		t.Fatal(err)
	}
	defer clientB.Disconnect()

	aDesired := fmt.Sprintf("homeassistant/sensor/%s/current/config", deviceA)
	aStale := fmt.Sprintf("homeassistant/sensor/%s/stale/config", deviceA)
	aAmbiguous := fmt.Sprintf("homeassistant/sensor/%s/ambiguous/config", deviceA)
	bCurrent := fmt.Sprintf("homeassistant/sensor/%s/current/config", deviceB)
	seeded := []string{aDesired, aStale, aAmbiguous, bCurrent}
	defer func() {
		for _, topic := range seeded {
			_ = clientA.Publish(topic, nil, true)
		}
	}()

	for topic, payload := range map[string][]byte{
		aDesired:   discoveryOwnershipPayload(t, originName, deviceA),
		aStale:     discoveryOwnershipPayload(t, originName, deviceA),
		aAmbiguous: []byte("{"),
		bCurrent:   discoveryOwnershipPayload(t, originName, deviceB),
	} {
		if err := clientA.Publish(topic, payload, true); err != nil {
			t.Fatalf("seed %s: %v", topic, err)
		}
	}

	liveDone := make(chan error, 1)
	go func() {
		liveDone <- clientB.Publish(deviceB+"/state", []byte("live"), false)
	}()
	clientA.purgeStaleDiscovery(map[string]bool{aDesired: true})
	if err := <-liveDone; err != nil {
		t.Fatalf("device B live publish: %v", err)
	}

	retained, err := clientA.CollectRetained("homeassistant/+/"+deviceA+"/+/config", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := retained[aStale]; ok {
		t.Fatalf("stale owned topic %s was not deleted", aStale)
	}
	if string(retained[aDesired]) == "" || string(retained[aAmbiguous]) == "" {
		t.Fatalf("device A desired or ambiguous record was removed: %#v", retained)
	}
	retainedB, err := clientB.CollectRetained("homeassistant/+/"+deviceB+"/+/config", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if string(retainedB[bCurrent]) == "" {
		t.Fatalf("device B retained record was removed: %#v", retainedB)
	}
}

func loadIntegrationConfig(t *testing.T, broker string, source config.IdentitySource) *config.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	contents := fmt.Sprintf("mqtt:\n  broker: %q\nupdates:\n  enabled: false\nmonitor_updates:\n  enabled: false\n", broker)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadWithIdentitySource(path, source)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func newIntegrationObserver(t *testing.T, broker string) paho.Client {
	t.Helper()
	options := paho.NewClientOptions().
		AddBroker(broker).
		SetClientID(fmt.Sprintf("observer-%d", time.Now().UnixNano())).
		SetConnectTimeout(5 * time.Second)
	client := paho.NewClient(options)
	token := client.Connect()
	if !token.WaitTimeout(10 * time.Second) {
		t.Fatal("observer MQTT connect timed out")
	}
	if err := token.Error(); err != nil {
		t.Fatalf("observer MQTT connect: %v", err)
	}
	return client
}

func subscribeIntegrationTopics(t *testing.T, client paho.Client, topics ...string) <-chan string {
	t.Helper()
	received := make(chan string, 16)
	var mu sync.Mutex
	filters := make(map[string]byte, len(topics))
	for _, topic := range topics {
		filters[topic] = 0
	}
	token := client.SubscribeMultiple(filters, func(_ paho.Client, msg paho.Message) {
		mu.Lock()
		defer mu.Unlock()
		received <- string(msg.Payload())
	})
	if !token.WaitTimeout(5 * time.Second) {
		t.Fatal("observer subscribe timed out")
	}
	if err := token.Error(); err != nil {
		t.Fatalf("observer subscribe: %v", err)
	}
	return received
}

func publishAndAwait(t *testing.T, client *Client, topic string, payload []byte, received <-chan string) {
	t.Helper()
	if err := client.Publish(topic, payload, false); err != nil {
		t.Fatalf("publish %s: %v", topic, err)
	}
	select {
	case got := <-received:
		if got != string(payload) {
			t.Fatalf("received payload = %q, want %q", got, payload)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for payload on %s", topic)
	}
}
