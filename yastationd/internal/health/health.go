// Package health runs the liveness state machine from cheap TCP probes and a heartbeat file.
package health

import (
	"net"
	"os"
	"sync"
	"time"
)

type State int

const (
	Online State = iota
	DegradedNoPipeline
	DegradedNoBroker
	OfflineNoLAN
)

func (s State) String() string {
	switch s {
	case Online:
		return "online"
	case DegradedNoPipeline, DegradedNoBroker:
		return "degraded"
	default:
		return "offline"
	}
}

// Probe is one reachability target. Role decides which degraded state a failure implies.
type Probe struct {
	Name string
	Addr string // host:port
	Role Role
}

type Role int

const (
	RoleBroker Role = iota
	RolePipeline
	RoleLAN
)

type Monitor struct {
	mu       sync.Mutex
	probes   []Probe
	state    State
	hbFile   string
	onChange func(State)

	// BrokerUp is fed by the MQTT client so the broker's own connection state, not just a TCP
	// probe, drives the DEGRADED(no-broker) decision.
	brokerConnected bool
}

func New(hbFile string, onChange func(State)) *Monitor {
	return &Monitor{hbFile: hbFile, state: OfflineNoLAN, onChange: onChange}
}

func (m *Monitor) AddProbe(name, addr string, role Role) {
	m.mu.Lock()
	m.probes = append(m.probes, Probe{Name: name, Addr: addr, Role: role})
	m.mu.Unlock()
}

func (m *Monitor) SetBrokerConnected(up bool) {
	m.mu.Lock()
	m.brokerConnected = up
	m.mu.Unlock()
}

// BrokerConnected reports what the MQTT client last said about its connection.
func (m *Monitor) BrokerConnected() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.brokerConnected
}

func (m *Monitor) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// Recheck probes now, off the ticker (a state change elsewhere, e.g. the broker connecting, must
// not wait up to one interval to show).
func (m *Monitor) Recheck(dialTimeout time.Duration) { go m.tick(dialTimeout) }

// Run probes on a ticker until ctx is done. dialTimeout bounds each TCP attempt.
func (m *Monitor) Run(stop <-chan struct{}, interval, dialTimeout time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	m.tick(dialTimeout)
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			m.tick(dialTimeout)
		}
	}
}

func (m *Monitor) tick(dialTimeout time.Duration) {
	m.mu.Lock()
	probes := make([]Probe, len(m.probes))
	copy(probes, m.probes)
	brokerConnected := m.brokerConnected
	m.mu.Unlock()

	lanOK := false
	brokerProbeOK := true
	pipelineOK := true
	hasBroker, hasPipeline := false, false
	for i := range probes {
		ok := dialOK(probes[i].Addr, dialTimeout)
		if ok {
			lanOK = true
		}
		switch probes[i].Role {
		case RoleBroker:
			hasBroker = true
			brokerProbeOK = brokerProbeOK && ok
		case RolePipeline:
			hasPipeline = true
			pipelineOK = pipelineOK && ok
		case RoleLAN:
			// contributes to lanOK only
		}
	}

	st := Online
	switch {
	case !lanOK && len(probes) > 0:
		st = OfflineNoLAN
	case hasBroker && !(brokerProbeOK && brokerConnected):
		st = DegradedNoBroker
	case hasPipeline && !pipelineOK:
		st = DegradedNoPipeline
	}

	m.touchHeartbeat()

	m.mu.Lock()
	changed := st != m.state
	m.state = st
	cb := m.onChange
	m.mu.Unlock()
	if changed && cb != nil {
		cb(st)
	}
}

func (m *Monitor) touchHeartbeat() {
	if m.hbFile == "" {
		return
	}
	now := time.Now()
	if err := os.Chtimes(m.hbFile, now, now); err != nil {
		os.WriteFile(m.hbFile, []byte(now.Format(time.RFC3339)), 0o644)
	}
}

func dialOK(addr string, timeout time.Duration) bool {
	c, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return false
	}
	c.Close()
	return true
}
