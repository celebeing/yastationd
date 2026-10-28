package wake

import (
	"bytes"
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
		{Kind: "WAKE", At: 21620 * time.Millisecond, Phrase: "алиса", VQE: -1},
		{Kind: "CMD", At: 64940 * time.Millisecond, Phrase: "громче", VQE: -1},
		{Kind: "SUBHIT:WAKE", At: 28290 * time.Millisecond, Phrase: "алиса", VQE: -1},
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

func TestATLine(t *testing.T) {
	var got []Event
	s := &Spotter{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), OnEvent: func(e Event) { got = append(got, e) }}
	s.setRunning(true)
	for _, l := range []string{"AT 21620 345600", "WAKE 21620 алиса", "WAKE 23000 алиса", // no AT: unknown
		"AT 1 2", "CMD 64940 громче"} { // an AT of another event is not taken
		s.line(l)
	}
	if len(got) != 3 || got[0].VQE != 345600 || got[0].gen != s.gen || got[1].VQE != -1 || got[2].VQE != -1 {
		t.Fatalf("got %+v", got)
	}
}

// feed runs n bytes of vqe_0 through the reader, each sample's low byte its index/2 mod 256.
func feed(s *Spotter, from, n int) {
	b := make([]byte, n)
	for i := range b {
		if (from+i)%2 == 0 {
			b[i] = byte((from + i) / 2)
		}
	}
	s.readVQE(io.NopCloser(bytes.NewReader(b)))
}

func TestSubscribeVQEAt(t *testing.T) {
	s := &Spotter{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	s.setRunning(true)
	feed(s, 0, 32000*5) // 5 s: the ring keeps the last 4
	ev := Event{Kind: "WAKE", VQE: 16000 * 4, gen: s.gen}

	var got []byte
	unsub, from := s.SubscribeVQEAt(ev, 300*time.Millisecond, func(b []byte) { got = append(got, b...) })
	if from != 16000*4-4800 || len(got) != (16000*5-int(from))*2 || got[0] != byte(from) {
		t.Fatalf("from %d, %d bytes, first %d", from, len(got), got[0])
	}
	feed(s, 32000*5, 640)
	if len(got) != (16000*5-int(from))*2+640 {
		t.Fatalf("live audio not delivered: %d", len(got))
	}
	unsub()

	// Further back than the ring holds: from its start.
	got = nil
	unsub, from = s.SubscribeVQEAt(ev, 5*time.Second, func(b []byte) { got = append(got, b...) })
	if want := int64(16000*5+320) - ringMax/2; from != want || len(got) != ringMax {
		t.Fatalf("from %d (want %d), %d bytes", from, want, len(got))
	}
	unsub()

	// The WAKE line came before its audio: nothing until the start, then from it on.
	got = nil
	end := int64(16000*5 + 320)
	unsub, from = s.SubscribeVQEAt(Event{VQE: end + 1000, gen: s.gen}, 0, func(b []byte) { got = append(got, b...) })
	if from != end+1000 || len(got) != 0 {
		t.Fatalf("from %d, %d bytes early", from, len(got))
	}
	feed(s, int(end)*2, 3200) // 1600 samples: the start is 1000 in
	if len(got) != 1200 || got[0] != byte(end+1000) {
		t.Fatalf("%d bytes, first %d", len(got), got[0])
	}
	unsub()

	// A child restarted since the event: the plain pre-roll.
	s.setRunning(true)
	feed(s, 0, 32000)
	got = nil
	unsub, from = s.SubscribeVQEAt(ev, 300*time.Millisecond, func(b []byte) { got = append(got, b...) })
	if from != -1 || len(got) != 9600 {
		t.Fatalf("fallback: from %d, %d bytes", from, len(got))
	}
	unsub()
}
