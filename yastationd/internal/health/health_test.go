package health

import (
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestMonitorTransitionsAndHeartbeat(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	var changes []State
	hb := filepath.Join(t.TempDir(), "heartbeat")
	m := New(hb, func(s State) { changes = append(changes, s) })
	m.AddProbe("lan", ln.Addr().String(), RoleLAN)
	m.AddProbe("broker", ln.Addr().String(), RoleBroker)
	m.AddProbe("pipeline", "127.0.0.1:1", RolePipeline)

	m.tick(time.Second)
	if got := m.State(); got != DegradedNoBroker {
		t.Fatalf("state without MQTT connection = %v, want %v", got, DegradedNoBroker)
	}
	if _, err := os.Stat(hb); err != nil {
		t.Fatalf("heartbeat was not written: %v", err)
	}

	m.SetBrokerConnected(true)
	m.tick(time.Second)
	if got := m.State(); got != DegradedNoPipeline {
		t.Fatalf("state with unavailable pipeline = %v, want %v", got, DegradedNoPipeline)
	}

	m.probes = m.probes[:2]
	m.tick(time.Second)
	if got := m.State(); got != Online {
		t.Fatalf("state with all probes healthy = %v, want %v", got, Online)
	}
	m.tick(time.Second)

	if want := []State{DegradedNoBroker, DegradedNoPipeline, Online}; !reflect.DeepEqual(changes, want) {
		t.Fatalf("state changes = %v, want %v", changes, want)
	}
}

func TestRunChecksImmediatelyAndStops(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	changes := make(chan State, 2)
	m := New("", func(s State) { changes <- s })
	m.AddProbe("lan", ln.Addr().String(), RoleLAN)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		m.Run(stop, time.Hour, time.Second)
		close(done)
	}()
	select {
	case got := <-changes:
		if got != Online {
			t.Fatalf("initial state = %v, want %v", got, Online)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not make its immediate check")
	}
	close(stop)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not stop")
	}
	select {
	case got := <-changes:
		t.Fatalf("callback after stop: %v", got)
	case <-time.After(20 * time.Millisecond):
	}
}
