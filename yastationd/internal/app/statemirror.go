package app

import (
	"sync"
	"time"
)

// Everything the app publishes goes through publish/publishEvent, which remember the last value
// of each of this speaker's state objects before handing it to MQTT. The web API serves that
// mirror next to the MQTT discovery entities (docs/web-ui.md, "One definition for MQTT and the
// web"), so a control or sensor is described once — and it works with no broker at all.

type mirrored struct {
	value string
	at    time.Time
}

type stateMirror struct {
	mu sync.Mutex
	m  map[string]mirrored
}

func (s *stateMirror) set(obj, v string) {
	s.mu.Lock()
	if s.m == nil {
		s.m = map[string]mirrored{}
	}
	s.m[obj] = mirrored{v, time.Now()}
	s.mu.Unlock()
}

// snapshot copies the mirror.
func (s *stateMirror) snapshot() map[string]mirrored {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]mirrored, len(s.m))
	for k, v := range s.m {
		out[k] = v
	}
	return out
}

// publish sends a retained message (and mirrors it when it is one of our state topics).
func (a *App) publish(topic, payload string) {
	if obj := a.topics.StateObject(topic); obj != "" {
		a.states.set(obj, payload)
	}
	a.mqtt.Publish(topic, payload)
}

// publishEvent sends a non-retained message (mirrored the same way).
func (a *App) publishEvent(topic, payload string) {
	if obj := a.topics.StateObject(topic); obj != "" {
		a.states.set(obj, payload)
	}
	a.mqtt.PublishEvent(topic, payload)
}
