package wake

import (
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestLineParsing(t *testing.T) {
	var got []Event
	s := &Spotter{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), OnEvent: func(e Event) { got = append(got, e) }}
	for _, l := range []string{"READY", "WAKE 21620 алиса", "CMD 64940 громче", "SUBHIT WAKE 28290 алиса",
		"LEVEL 1000 vqe=-72 raw=-50 spk=-90", "WAKE x алиса", "garbage"} {
		s.line(l)
	}
	want := []Event{
		{"WAKE", 21620 * time.Millisecond, "алиса"},
		{"CMD", 64940 * time.Millisecond, "громче"},
		{"SUBHIT:WAKE", 28290 * time.Millisecond, "алиса"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestPrerollThenLive(t *testing.T) {
	s := &Spotter{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	s.ring = make([]byte, 16000) // 0.5 s buffered
	var n int
	unsub := s.SubscribeVQE(300*time.Millisecond, func(b []byte) { n += len(b) })
	if n != 9600 { // 0.3 s * 16000 * 2
		t.Fatalf("preroll bytes = %d", n)
	}
	unsub()
}
