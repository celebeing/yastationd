package wake

import (
	"bytes"
	"io"
	"log/slog"
	"testing"
)

func TestCallSubscribersReceiveFramesUntilUnsubscribed(t *testing.T) {
	s := &Spotter{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	var first, second [][]byte
	unsubFirst := s.SubscribeCall(func(b []byte) { first = append(first, append([]byte(nil), b...)) })
	s.SubscribeCall(func(b []byte) { second = append(second, append([]byte(nil), b...)) })

	frame := bytes.Repeat([]byte{0x42}, 640)
	s.readCall(io.NopCloser(bytes.NewReader(frame)))
	if len(first) != 1 || len(second) != 1 || !bytes.Equal(first[0], frame) || !bytes.Equal(second[0], frame) {
		t.Fatalf("subscribers received first=%d second=%d", len(first), len(second))
	}

	unsubFirst()
	s.readCall(io.NopCloser(bytes.NewReader(frame)))
	if len(first) != 1 || len(second) != 2 {
		t.Fatalf("unsubscribe delivery first=%d second=%d", len(first), len(second))
	}
}

func TestVQEReaderKeepsRingAndFeedsSubscribers(t *testing.T) {
	s := &Spotter{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	var got [][]byte
	unsub := s.SubscribeVQE(0, func(b []byte) { got = append(got, append([]byte(nil), b...)) })
	defer unsub()

	first := bytes.Repeat([]byte{0x11}, 640)
	second := bytes.Repeat([]byte{0x22}, 640)
	s.readVQE(io.NopCloser(bytes.NewReader(append(first, second...))))
	if len(got) != 2 || !bytes.Equal(got[0], first) || !bytes.Equal(got[1], second) {
		t.Fatalf("VQE frames = %d", len(got))
	}
	if s.ringEnd != int64(len(first)+len(second)) || !bytes.Equal(s.ring, append(first, second...)) {
		t.Fatalf("ring end=%d bytes=%d", s.ringEnd, len(s.ring))
	}
}

func TestFeedDropsWhenSpotterLagsAndCallModeNeedsChild(t *testing.T) {
	s := &Spotter{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), feed: make(chan []byte, 1)}
	s.Feed([]byte{1})
	s.Feed([]byte{2})
	if got := <-s.feed; !bytes.Equal(got, []byte{1}) {
		t.Fatalf("queued frame = %v", got)
	}
	s.mu.Lock()
	drops, callMode := s.drops, s.callMode
	s.mu.Unlock()
	if drops != 1 {
		t.Fatalf("dropped frames = %d, want 1", drops)
	}
	if s.CallMode(true) {
		t.Fatal("call mode succeeded without a running child")
	}
	s.mu.Lock()
	callMode = s.callMode
	s.mu.Unlock()
	if !callMode {
		t.Fatal("requested call mode was not remembered")
	}
}
