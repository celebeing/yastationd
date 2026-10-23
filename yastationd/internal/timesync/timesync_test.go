package timesync

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTimestampRoundTrip(t *testing.T) {
	in := time.Date(2026, 10, 23, 0, 1, 2, 345678000, time.UTC)
	b := make([]byte, 8)
	putTimestamp(b, in)
	if d := timestamp(b).Sub(in); d < -time.Microsecond || d > time.Microsecond {
		t.Fatalf("round trip off by %v", d)
	}
}

func TestChronyServersAndGateway(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "chrony.conf")
	os.WriteFile(conf, []byte("# c\nserver 10.0.0.1 iburst\npool pool.example\ndriftfile /x\n"), 0o644)
	if got := chronyServers(conf); len(got) != 2 || got[0] != "10.0.0.1" || got[1] != "pool.example" {
		t.Fatalf("chronyServers = %v", got)
	}
	route := filepath.Join(dir, "proc/net/route")
	os.MkdirAll(filepath.Dir(route), 0o755)
	os.WriteFile(route, []byte("Iface\tDestination\tGateway \tFlags\n"+
		"wlan0\t000200C0\t00000000\t0001\n"+
		"wlan0\t00000000\t010200C0\t0003\n"), 0o644)
	if got := defaultGateway(route); got != "192.0.2.1" {
		t.Fatalf("defaultGateway = %q", got)
	}

	s := &Syncer{Servers: []string{"10.0.0.1", "ntp.example"}, ChronyConf: conf, Root: dir}
	want := []string{"10.0.0.1", "ntp.example", "pool.example", "192.0.2.1"}
	got := s.candidates()
	if len(got) != len(want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("candidates = %v, want %v", got, want)
		}
	}
}

func TestTimeDaemon(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "proc/42"), 0o755)
	os.WriteFile(filepath.Join(dir, "proc/42/comm"), []byte("sh\n"), 0o644)
	s := &Syncer{Root: dir}
	if d := s.timeDaemon(); d != "" {
		t.Fatalf("timeDaemon = %q", d)
	}
	os.MkdirAll(filepath.Join(dir, "proc/7"), 0o755)
	os.WriteFile(filepath.Join(dir, "proc/7/comm"), []byte("chronyd\n"), 0o644)
	if d := s.timeDaemon(); d != "chronyd" {
		t.Fatalf("timeDaemon = %q", d)
	}
}

// fakeServer answers SNTP with the local clock shifted by skew.
func fakeServer(t *testing.T, skew time.Duration, stratum byte) string {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 128)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			if n < 48 {
				continue
			}
			resp := make([]byte, 48)
			resp[0] = 0x24 // version 4, mode 4
			resp[1] = stratum
			copy(resp[24:32], buf[40:48])
			now := time.Now().Add(skew)
			putTimestamp(resp[32:], now)
			putTimestamp(resp[40:], now)
			pc.WriteTo(resp, addr)
		}
	}()
	return pc.LocalAddr().String()
}

func TestQueryOffset(t *testing.T) {
	addr := fakeServer(t, 5*time.Minute, 2)
	off, err := Query(addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if d := off - 5*time.Minute; d < -50*time.Millisecond || d > 50*time.Millisecond {
		t.Fatalf("offset = %v, want ~5m", off)
	}
	if _, err := Query(fakeServer(t, 0, 0), time.Second); err == nil {
		t.Fatal("stratum 0 accepted")
	}
}

func TestSyncOnceFallsThrough(t *testing.T) {
	var stepped time.Duration
	var changes []bool
	s := &Syncer{
		Servers:  []string{"dead", "good"},
		Root:     t.TempDir(),
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		OnChange: func(v bool) { changes = append(changes, v) },
		query: func(srv string) (time.Duration, error) {
			if srv == "dead" {
				return 0, errors.New("timeout")
			}
			return 6 * time.Minute, nil
		},
		step: func(off time.Duration) error { stepped = off; return nil },
	}
	if !s.syncOnce() || stepped != 6*time.Minute || !s.Synced() {
		t.Fatalf("sync: stepped=%v synced=%v", stepped, s.Synced())
	}
	if len(changes) != 1 || !changes[0] {
		t.Fatalf("OnChange calls = %v", changes)
	}
	// A small offset is not stepped.
	stepped = 0
	s.query = func(string) (time.Duration, error) { return 50 * time.Millisecond, nil }
	if !s.syncOnce() || stepped != 0 {
		t.Fatalf("small offset stepped: %v", stepped)
	}
}

func TestCheckDaemon(t *testing.T) {
	var stepped time.Duration
	kernel := false
	answer := errors.New("timeout")
	off := 41 * time.Hour
	s := &Syncer{
		Servers:      []string{"lan"},
		Root:         t.TempDir(),
		Log:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		query:        func(string) (time.Duration, error) { return off, answer },
		step:         func(o time.Duration) error { stepped = o; return nil },
		kernelSynced: func() bool { return kernel },
	}
	// No server yet, kernel unsynced: stays unsynced and retries soon.
	if w := s.checkDaemon(); s.Synced() || w != retryMin {
		t.Fatalf("no answer, kernel unsynced: synced=%v wait=%v", s.Synced(), w)
	}
	// No server, kernel synced: trusted, but still retried soon until a server confirms.
	kernel = true
	if w := s.checkDaemon(); !s.Synced() || w != retryMin {
		t.Fatalf("no answer, kernel synced: synced=%v wait=%v", s.Synced(), w)
	}
	// The server answers with a stale-boot offset: we step it, even with the daemon running.
	answer = nil
	if w := s.checkDaemon(); stepped != off || !s.Synced() || w != daemonPoll {
		t.Fatalf("big offset: stepped=%v synced=%v wait=%v", stepped, s.Synced(), w)
	}
	// Drift-sized offsets are the daemon's business.
	stepped, off = 0, 2*time.Second
	if s.checkDaemon(); stepped != 0 || !s.Synced() {
		t.Fatalf("small offset stepped: %v", stepped)
	}
	// Once confirmed, a server outage doesn't flip it back.
	answer, kernel = errors.New("timeout"), false
	if w := s.checkDaemon(); !s.Synced() || w != daemonPoll {
		t.Fatalf("outage after confirm: synced=%v wait=%v", s.Synced(), w)
	}
}
