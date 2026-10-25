// Package mqtt wraps paho: LWT availability, backoff reconnect, and a re-publish hook that
// re-sends discovery + current state on every (re)connect.
package mqtt

import (
	"fmt"
	"log/slog"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

type Client struct {
	c      paho.Client
	topics Topics
	log    *slog.Logger

	onConnect func() // re-publish discovery + state
	onBroker  func(bool)
}

type Options struct {
	Host, User, Pass string
	Port             int
	ClientID         string
	Topics           Topics
	Log              *slog.Logger
	OnConnect        func()
	OnBroker         func(connected bool)
}

func New(o Options) *Client {
	cl := &Client{topics: o.Topics, log: o.Log, onConnect: o.OnConnect, onBroker: o.OnBroker}

	opts := paho.NewClientOptions().
		AddBroker(fmt.Sprintf("tcp://%s:%d", o.Host, o.Port)).
		SetClientID(o.ClientID).
		SetUsername(o.User).
		SetPassword(o.Pass).
		SetCleanSession(true).
		SetOrderMatters(false).
		SetKeepAlive(30*time.Second).
		SetConnectTimeout(10*time.Second).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5*time.Second).
		SetMaxReconnectInterval(60*time.Second).
		SetWill(o.Topics.Availability(), "offline", 1, true)

	opts.OnConnect = func(paho.Client) {
		cl.log.Info("mqtt connected")
		if cl.onBroker != nil {
			cl.onBroker(true)
		}
		cl.publish(o.Topics.Availability(), "online", true)
		if cl.onConnect != nil {
			cl.onConnect()
		}
	}
	opts.OnConnectionLost = func(_ paho.Client, err error) {
		cl.log.Warn("mqtt connection lost", "err", err)
		if cl.onBroker != nil {
			cl.onBroker(false)
		}
	}
	cl.c = paho.NewClient(opts)
	return cl
}

// Connect kicks off the retrying connect; it returns immediately because ConnectRetry is on.
func (c *Client) Connect() { c.c.Connect() }

// Disconnect marks the device offline and disconnects. A clean DISCONNECT makes the broker drop
// the LWT, so "offline" must be published (and acked) explicitly before closing.
func (c *Client) Disconnect() {
	if c.c.IsConnected() {
		t := c.c.Publish(c.topics.Availability(), 1, true, "offline")
		if !t.WaitTimeout(2*time.Second) || t.Error() != nil {
			c.log.Warn("mqtt: offline not confirmed before disconnect", "err", t.Error())
		}
	}
	c.c.Disconnect(250)
}

func (c *Client) publish(topic, payload string, retain bool) {
	t := c.c.Publish(topic, 1, retain, payload)
	go func() {
		if t.WaitTimeout(5*time.Second) && t.Error() != nil {
			c.log.Warn("mqtt publish failed", "topic", topic, "err", t.Error())
		}
	}()
}

// Publish a state value (QoS1, retained).
func (c *Client) Publish(topic, payload string) { c.publish(topic, payload, true) }

// PublishEvent sends a non-retained event (e.g. a button press).
func (c *Client) PublishEvent(topic, payload string) { c.publish(topic, payload, false) }

func (c *Client) Subscribe(topic string, fn func(payload []byte)) {
	c.c.Subscribe(topic, 1, func(_ paho.Client, m paho.Message) { fn(m.Payload()) })
}

// SubscribeTopic is Subscribe with the message's topic (for wildcard subscriptions).
func (c *Client) SubscribeTopic(topic string, fn func(topic string, payload []byte)) {
	c.c.Subscribe(topic, 1, func(_ paho.Client, m paho.Message) { fn(m.Topic(), m.Payload()) })
}

func (c *Client) Connected() bool { return c.c.IsConnected() }
