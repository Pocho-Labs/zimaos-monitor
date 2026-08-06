package mqtt

import (
	"errors"
	"slices"
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
	"zimaos-monitor/internal/config"
)

type fakeToken struct {
	err  error
	done chan struct{}
}

func newFakeToken(err error) *fakeToken {
	done := make(chan struct{})
	close(done)
	return &fakeToken{err: err, done: done}
}

func (t *fakeToken) Wait() bool                     { return true }
func (t *fakeToken) WaitTimeout(time.Duration) bool { return true }
func (t *fakeToken) Done() <-chan struct{}          { return t.done }
func (t *fakeToken) Error() error                   { return t.err }

type fakeMessage struct {
	topic    string
	payload  []byte
	retained bool
}

func (m *fakeMessage) Duplicate() bool   { return false }
func (m *fakeMessage) Qos() byte         { return 0 }
func (m *fakeMessage) Retained() bool    { return m.retained }
func (m *fakeMessage) Topic() string     { return m.topic }
func (m *fakeMessage) MessageID() uint16 { return 0 }
func (m *fakeMessage) Payload() []byte   { return m.payload }
func (m *fakeMessage) Ack()              {}

type fakePahoClient struct {
	messages       []*fakeMessage
	subscribeErr   error
	unsubscribeErr error
	unsubscribed   []string
	afterDelivery  func()
}

func (c *fakePahoClient) IsConnected() bool      { return true }
func (c *fakePahoClient) IsConnectionOpen() bool { return true }
func (c *fakePahoClient) Connect() paho.Token    { return newFakeToken(nil) }
func (c *fakePahoClient) Disconnect(uint)        {}
func (c *fakePahoClient) Publish(string, byte, bool, interface{}) paho.Token {
	return newFakeToken(nil)
}
func (c *fakePahoClient) Subscribe(_ string, _ byte, handler paho.MessageHandler) paho.Token {
	if c.subscribeErr == nil {
		for _, message := range c.messages {
			handler(c, message)
		}
		if c.afterDelivery != nil {
			c.afterDelivery()
		}
	}
	return newFakeToken(c.subscribeErr)
}
func (c *fakePahoClient) SubscribeMultiple(map[string]byte, paho.MessageHandler) paho.Token {
	return newFakeToken(nil)
}
func (c *fakePahoClient) Unsubscribe(topics ...string) paho.Token {
	c.unsubscribed = append(c.unsubscribed, topics...)
	return newFakeToken(c.unsubscribeErr)
}
func (c *fakePahoClient) AddRoute(string, paho.MessageHandler) {}
func (c *fakePahoClient) OptionsReader() paho.ClientOptionsReader {
	return paho.ClientOptionsReader{}
}

func TestCollectRetainedIgnoresLiveAndCopiesPayload(t *testing.T) {
	retainedPayload := []byte("retained")
	fake := &fakePahoClient{
		messages: []*fakeMessage{
			{topic: "test/retained", payload: retainedPayload, retained: true},
			{topic: "test/live", payload: []byte("live"), retained: false},
		},
		afterDelivery: func() {
			retainedPayload[0] = 'X'
		},
	}
	client := &Client{inner: fake, cfg: &config.Config{}}
	collected, err := client.CollectRetained("test/#", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if string(collected["test/retained"]) != "retained" {
		t.Fatalf("retained payload = %q, want copied original", collected["test/retained"])
	}
	if _, ok := collected["test/live"]; ok {
		t.Fatal("live message entered retained collection")
	}
	if !slices.Equal(fake.unsubscribed, []string{"test/#"}) {
		t.Fatalf("unsubscribed = %#v", fake.unsubscribed)
	}
}

func TestCollectRetainedReportsSubscribeAndUnsubscribeErrors(t *testing.T) {
	t.Run("subscribe", func(t *testing.T) {
		fake := &fakePahoClient{subscribeErr: errors.New("subscribe failed")}
		client := &Client{inner: fake, cfg: &config.Config{}}
		if _, err := client.CollectRetained("test/#", 20*time.Millisecond); err == nil {
			t.Fatal("expected subscribe error")
		}
	})
	t.Run("unsubscribe", func(t *testing.T) {
		fake := &fakePahoClient{unsubscribeErr: errors.New("unsubscribe failed")}
		client := &Client{inner: fake, cfg: &config.Config{}}
		if _, err := client.CollectRetained("test/#", 20*time.Millisecond); err == nil {
			t.Fatal("expected unsubscribe error")
		}
		if !slices.Equal(fake.unsubscribed, []string{"test/#"}) {
			t.Fatalf("unsubscribed = %#v", fake.unsubscribed)
		}
	})
}

func TestCollectRetainedNoMessagesHonorsTimeout(t *testing.T) {
	fake := &fakePahoClient{}
	client := &Client{inner: fake, cfg: &config.Config{}}
	start := time.Now()
	if _, err := client.CollectRetained("test/#", 20*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("collection took %v, expected timeout termination", elapsed)
	}
}
