package sendspin

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func fakeHost(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sendspin-host")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestHostRelaysEventsAndCommands(t *testing.T) {
	started := make(chan struct{}, 1)
	events := make(chan Event, 2)
	h := &Host{
		Bin:     fakeHost(t, "echo 'READY 1'; IFS= read -r line; echo \"COMMAND $line\""),
		Args:    func() []string { return nil },
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		OnStart: func() { started <- struct{}{} },
		OnEvent: func(e Event) { events <- e },
	}
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- h.runOnce(stop) }()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("OnStart was not called")
	}
	h.Send("VOLUME %d", 37)

	var got []Event
	for len(got) < 2 {
		select {
		case e := <-events:
			got = append(got, e)
		case <-time.After(time.Second):
			t.Fatalf("events = %#v", got)
		}
	}
	if got[0].Kind != "READY" || got[0].Int(0, 0) != 1 || got[1].Kind != "COMMAND" || got[1].Rest != "VOLUME 37" {
		t.Fatalf("events = %#v", got)
	}
	if err := <-done; err != nil {
		t.Fatalf("runOnce = %v", err)
	}
	h.Send("dropped after exit")
}

func TestEventIntFallsBackForMissingOrMalformedFields(t *testing.T) {
	e := Event{Fields: []string{"12", "bad"}}
	if got := e.Int(0, -1); got != 12 {
		t.Fatalf("valid field = %d", got)
	}
	for _, i := range []int{1, 2} {
		if got := e.Int(i, -1); got != -1 {
			t.Errorf("field %d = %d, want fallback", i, got)
		}
	}
}

func TestSupervisorBackoff(t *testing.T) {
	now := time.Unix(1_000, 0)
	if got := retryDelay(8*time.Second, now, now.Add(time.Minute+time.Nanosecond)); got != time.Second {
		t.Fatalf("stable restart delay = %v, want 1s", got)
	}
	if got := retryDelay(8*time.Second, now, now.Add(time.Minute)); got != 8*time.Second {
		t.Fatalf("short run delay = %v, want 8s", got)
	}
	if got := nextBackoff(32 * time.Second); got != time.Minute+4*time.Second {
		t.Fatalf("backoff = %v", got)
	}
	if got := nextBackoff(time.Minute + 4*time.Second); got != time.Minute+4*time.Second {
		t.Fatalf("capped backoff = %v", got)
	}
}

func TestRunRestartsAndStopsDuringBackoff(t *testing.T) {
	starts := make(chan struct{}, 2)
	type wait struct {
		delay time.Duration
		done  chan time.Time
	}
	waits := make(chan wait, 2)
	h := &Host{
		Bin: fakeHost(t, "exit 1"),
		Args: func() []string {
			starts <- struct{}{}
			return nil
		},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		after: func(d time.Duration) <-chan time.Time {
			w := wait{delay: d, done: make(chan time.Time, 1)}
			waits <- w
			return w.done
		},
	}
	stop := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		h.Run(stop)
		close(finished)
	}()
	select {
	case <-starts:
	case <-time.After(time.Second):
		t.Fatal("first child did not start")
	}
	var first wait
	select {
	case first = <-waits:
	case <-time.After(time.Second):
		t.Fatal("first restart was not scheduled")
	}
	if first.delay != time.Second {
		t.Fatalf("first delay = %v", first.delay)
	}
	first.done <- time.Now()
	select {
	case <-starts:
	case <-time.After(time.Second):
		t.Fatal("child did not restart")
	}
	var second wait
	select {
	case second = <-waits:
	case <-time.After(time.Second):
		t.Fatal("second restart was not scheduled")
	}
	if second.delay != 2*time.Second {
		t.Fatalf("second delay = %v", second.delay)
	}
	close(stop)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("Run did not stop during backoff")
	}
}
