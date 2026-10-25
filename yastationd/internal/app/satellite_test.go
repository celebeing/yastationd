package app

import (
	"sync"
	"testing"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/display"
)

// fakeAplay records when bytes arrive.
type fakeAplay struct {
	mu     sync.Mutex
	n      int
	first  time.Time
	closed chan struct{}
}

func (f *fakeAplay) Write(b []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.n == 0 {
		f.first = time.Now()
	}
	f.n += len(b)
	return len(b), nil
}

func (f *fakeAplay) Close() error { close(f.closed); return nil }

func (f *fakeAplay) bytes() int { f.mu.Lock(); defer f.mu.Unlock(); return f.n }

func TestPumpJitterBuffer(t *testing.T) {
	a := &App{ringStack: display.NewStack(func(time.Time) display.RingFrame { return nil }),
		matrixStack: display.NewStack(func(time.Time) display.FrontFrame { return display.FrontFrame{} })}
	s := &satellite{a: a}
	// 8 kHz mono: 16000 B/s, so the 0.5 s prebuffer is 8000 B and a 1600 B chunk is 0.1 s.
	st := &sndStream{rate: 8000, channels: 1, q: make(chan []byte, 64), done: make(chan struct{}), start: time.Now()}
	s.cur = st
	w := &fakeAplay{closed: make(chan struct{})}
	go s.pump(st, w)
	chunk := make([]byte, 1600)

	for i := 0; i < 4; i++ { // 0.4 s: below the prebuffer, nothing may reach aplay yet
		st.q <- chunk
	}
	time.Sleep(50 * time.Millisecond)
	if n := w.bytes(); n != 0 {
		t.Fatalf("wrote %d bytes before the prebuffer filled", n)
	}
	st.q <- chunk // 0.5 s: plays
	time.Sleep(50 * time.Millisecond)
	if n := w.bytes(); n != 8000 {
		t.Fatalf("after prebuffer: %d bytes, want 8000", n)
	}
	time.Sleep(700 * time.Millisecond) // stall longer than what was written: the pump must notice
	if st.rebuffers.Load() != 1 {
		t.Fatalf("rebuffers = %d, want 1", st.rebuffers.Load())
	}
	st.q <- chunk // held back again (below the prebuffer) ...
	time.Sleep(50 * time.Millisecond)
	if n := w.bytes(); n != 8000 {
		t.Fatalf("wrote during rebuffer: %d", n)
	}
	close(st.q) // ... and played out at the end of the stream
	select {
	case <-w.closed:
	case <-time.After(time.Second):
		t.Fatal("pump did not finish")
	}
	if n := w.bytes(); n != 9600 {
		t.Fatalf("total %d bytes, want 9600", n)
	}
}

func TestPumpTrickleStillPlays(t *testing.T) {
	a := &App{ringStack: display.NewStack(func(time.Time) display.RingFrame { return nil }),
		matrixStack: display.NewStack(func(time.Time) display.FrontFrame { return display.FrontFrame{} })}
	s := &satellite{a: a}
	st := &sndStream{rate: 8000, channels: 1, q: make(chan []byte, 64), done: make(chan struct{}), start: time.Now()}
	s.cur = st
	w := &fakeAplay{closed: make(chan struct{})}
	go s.pump(st, w)
	st.q <- make([]byte, 1600) // never reaches the prebuffer: played after rebufferWait
	time.Sleep(rebufferWait + 200*time.Millisecond)
	if n := w.bytes(); n != 1600 {
		t.Fatalf("trickle: %d bytes after the wait, want 1600", n)
	}
	close(st.q)
	<-w.closed
}
