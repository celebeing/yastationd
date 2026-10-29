package mqtt

import (
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

type recordedPublish struct {
	topic    string
	qos      byte
	retained bool
	payload  any
}

type fakeClient struct {
	mu            sync.Mutex
	connected     bool
	open          bool
	connects      int
	disconnects   []uint
	publishes     []recordedPublish
	subscriptions map[string]paho.MessageHandler
	operations    []string
}

func (c *fakeClient) Connect() paho.Token {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connects++
	c.operations = append(c.operations, "connect")
	return completedToken{}
}

func (c *fakeClient) Disconnect(quiesce uint) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.disconnects = append(c.disconnects, quiesce)
	c.operations = append(c.operations, "disconnect")
}

func (c *fakeClient) Publish(topic string, qos byte, retained bool, payload any) paho.Token {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.publishes = append(c.publishes, recordedPublish{topic, qos, retained, payload})
	c.operations = append(c.operations, "publish")
	return completedToken{}
}

func (c *fakeClient) Subscribe(topic string, qos byte, callback paho.MessageHandler) paho.Token {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.subscriptions == nil {
		c.subscriptions = map[string]paho.MessageHandler{}
	}
	c.subscriptions[topic] = callback
	c.operations = append(c.operations, "subscribe")
	return completedToken{}
}

func (c *fakeClient) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

func (c *fakeClient) IsConnectionOpen() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.open
}

func (c *fakeClient) snapshot() (int, []uint, []recordedPublish, map[string]paho.MessageHandler, []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	subs := make(map[string]paho.MessageHandler, len(c.subscriptions))
	for topic, handler := range c.subscriptions {
		subs[topic] = handler
	}
	return c.connects, append([]uint(nil), c.disconnects...), append([]recordedPublish(nil), c.publishes...), subs, append([]string(nil), c.operations...)
}

type completedToken struct{}

func (completedToken) Wait() bool                     { return true }
func (completedToken) WaitTimeout(time.Duration) bool { return true }
func (completedToken) Done() <-chan struct{}          { ch := make(chan struct{}); close(ch); return ch }
func (completedToken) Error() error                   { return nil }

type testMessage struct {
	topic   string
	payload []byte
}

func (m testMessage) Duplicate() bool   { return false }
func (m testMessage) Qos() byte         { return 1 }
func (m testMessage) Retained() bool    { return false }
func (m testMessage) Topic() string     { return m.topic }
func (m testMessage) MessageID() uint16 { return 0 }
func (m testMessage) Payload() []byte   { return m.payload }
func (testMessage) Ack()                {}

func testClient(c *fakeClient) *Client {
	return &Client{
		c:      c,
		topics: NewTopics("yastation", "homeassistant", "kitchen"),
		log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func TestClientPublishesAndDisconnectsInOrder(t *testing.T) {
	fake := &fakeClient{connected: true, open: true}
	c := testClient(fake)
	c.Connect()
	c.Publish("state/topic", "state")
	c.PublishEvent("event/topic", "event")
	c.Disconnect()

	connects, disconnects, publishes, _, operations := fake.snapshot()
	if connects != 1 || len(disconnects) != 1 || disconnects[0] != 250 {
		t.Fatalf("connect/disconnect = %d/%v", connects, disconnects)
	}
	want := []recordedPublish{
		{topic: "state/topic", qos: 1, retained: true, payload: "state"},
		{topic: "event/topic", qos: 1, retained: false, payload: "event"},
		{topic: "yastation/kitchen/availability", qos: 1, retained: true, payload: "offline"},
	}
	if len(publishes) != len(want) {
		t.Fatalf("publishes = %#v", publishes)
	}
	for i := range want {
		if publishes[i] != want[i] {
			t.Errorf("publish %d = %#v, want %#v", i, publishes[i], want[i])
		}
	}
	if want := []string{"connect", "publish", "publish", "publish", "disconnect"}; len(operations) != len(want) {
		t.Fatalf("operations = %v, want %v", operations, want)
	} else {
		for i := range want {
			if operations[i] != want[i] {
				t.Errorf("operation %d = %q, want %q", i, operations[i], want[i])
			}
		}
	}
}

func TestClientSubscriptionsAndConnectionState(t *testing.T) {
	fake := &fakeClient{open: true}
	c := testClient(fake)
	var payload []byte
	var topic string
	c.Subscribe("plain", func(got []byte) { payload = append([]byte(nil), got...) })
	c.SubscribeTopic("wild/#", func(gotTopic string, got []byte) {
		topic = gotTopic
		payload = append([]byte(nil), got...)
	})
	_, _, _, subscriptions, _ := fake.snapshot()
	subscriptions["plain"](nil, testMessage{topic: "plain", payload: []byte("one")})
	if string(payload) != "one" {
		t.Fatalf("plain payload = %q", payload)
	}
	subscriptions["wild/#"](nil, testMessage{topic: "wild/two", payload: []byte("two")})
	if topic != "wild/two" || string(payload) != "two" {
		t.Fatalf("topic payload = %q %q", topic, payload)
	}
	if !c.Connected() {
		t.Fatal("open connection reported disconnected")
	}
	fake.mu.Lock()
	fake.open = false
	fake.mu.Unlock()
	if c.Connected() {
		t.Fatal("closed connection reported open")
	}
}

func TestNewWiresConnectionCallbacks(t *testing.T) {
	oldFactory := newPahoClient
	defer func() { newPahoClient = oldFactory }()
	fake := &fakeClient{}
	var options *paho.ClientOptions
	newPahoClient = func(got *paho.ClientOptions) mqttClient {
		options = got
		return fake
	}
	var callbacks []string
	c := New(Options{
		Host: "broker", Port: 1883, ClientID: "kitchen", User: "user", Pass: "pass",
		Topics:    NewTopics("yastation", "homeassistant", "kitchen"),
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		OnBroker:  func(up bool) { callbacks = append(callbacks, map[bool]string{true: "up", false: "down"}[up]) },
		OnConnect: func() { callbacks = append(callbacks, "connect") },
	})
	if c.c != fake || options == nil {
		t.Fatal("New did not use the client factory")
	}
	reader := paho.NewOptionsReader(options)
	servers := reader.Servers()
	if len(servers) != 1 || servers[0].String() != "tcp://broker:1883" ||
		reader.ClientID() != "kitchen" || reader.Username() != "user" || reader.Password() != "pass" {
		t.Fatalf("connection options = %#v", options)
	}
	if !reader.CleanSession() || reader.Order() || reader.KeepAlive() != 30*time.Second ||
		reader.ConnectTimeout() != 10*time.Second || !reader.AutoReconnect() || !reader.ConnectRetry() ||
		reader.ConnectRetryInterval() != 5*time.Second || reader.MaxReconnectInterval() != 60*time.Second {
		t.Fatalf("reconnect options = %#v", options)
	}
	if !reader.WillEnabled() || reader.WillTopic() != "yastation/kitchen/availability" ||
		string(reader.WillPayload()) != "offline" || reader.WillQos() != 1 || !reader.WillRetained() {
		t.Fatalf("last will = %#v", options)
	}
	options.OnConnect(nil)
	options.OnConnectionLost(nil, errors.New("network lost"))
	_, _, publishes, _, _ := fake.snapshot()
	if len(publishes) != 1 || publishes[0] != (recordedPublish{topic: "yastation/kitchen/availability", qos: 1, retained: true, payload: "online"}) {
		t.Fatalf("connect publish = %#v", publishes)
	}
	if want := []string{"up", "connect", "down"}; len(callbacks) != len(want) {
		t.Fatalf("callbacks = %v, want %v", callbacks, want)
	} else {
		for i := range want {
			if callbacks[i] != want[i] {
				t.Errorf("callback %d = %q, want %q", i, callbacks[i], want[i])
			}
		}
	}
}

func TestNilClientPublishIsLocalOnly(t *testing.T) {
	var c *Client
	c.Publish("state", "value")
	c.PublishEvent("event", "value")
}
