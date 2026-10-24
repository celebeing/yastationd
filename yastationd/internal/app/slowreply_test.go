package app

import (
	"strings"
	"testing"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/config"
)

func TestHeldWakeBuffersThenForwards(t *testing.T) {
	h := &heldWake{}
	h.add([]byte{1})
	h.add([]byte{2})
	var got []byte
	h.attach(func(b []byte) { got = append(got, b...) })
	h.add([]byte{3}) // live audio after attach goes straight through
	if string(got) != "\x01\x02\x03" {
		t.Fatalf("forwarded %v", got)
	}
}

func TestIsStop(t *testing.T) {
	for _, p := range []string{"стоп", "Хватит", "отмена"} {
		if !isStop(p) {
			t.Errorf("%q is a stop", p)
		}
	}
	if isStop("громче") || isStop("пауза") {
		t.Error("громче/пауза must not cancel a pending reply")
	}
}

func TestSlowReplyConfig(t *testing.T) {
	c, err := config.Parse(strings.NewReader("reply_timeout = 45\nwake_while_waiting = ignore\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.ReplyTimeout != 45*time.Second || c.WakeWhileWaiting != "ignore" {
		t.Fatalf("parsed %v %q", c.ReplyTimeout, c.WakeWhileWaiting)
	}
	if d := config.Default(); d.ReplyTimeout != 90*time.Second || d.WakeWhileWaiting != "interrupt" {
		t.Fatalf("defaults %v %q", d.ReplyTimeout, d.WakeWhileWaiting)
	}
	if _, err := config.Parse(strings.NewReader("wake_while_waiting = maybe\n")); err == nil {
		t.Fatal("bad policy accepted")
	}
}

func TestPendingReplyTimesOut(t *testing.T) {
	a := newExpressApp(t, true)
	a.cfg.ReplyTimeout = 50 * time.Millisecond
	a.sat = &satellite{a: a}
	a.sat.setAwaiting(true)
	if !a.sat.awaitingReply() || !a.sat.inDialog() {
		t.Fatal("not pending after the transcript")
	}
	time.Sleep(150 * time.Millisecond)
	a.sat.mu.Lock()
	cancelled := a.sat.awaitUntil.IsZero() // cancelReply ran (not just the clock passing)
	a.sat.mu.Unlock()
	if !cancelled {
		t.Fatal("the reply deadline did not cancel the wait")
	}
	if !a.matrixStack.Has("voice") { // the sad face
		t.Fatal("no indication that the wait was given up")
	}
}
