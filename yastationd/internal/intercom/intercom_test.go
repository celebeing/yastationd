package intercom

import (
	"math"
	"math/rand"
	"net"
	"sync"
	"testing"
	"time"
)

const testSecret = "a-test-secret-of-some-length"

func sine(n, phase int, amp float64) []int16 {
	f := make([]int16, n)
	for i := range f {
		f[i] = int16(amp * 32767 * math.Sin(2*math.Pi*440*float64(phase+i)/Rate))
	}
	return f
}

func TestUnwrap(t *testing.T) {
	h := firstIndex(0xfff0)
	for k := 1; k < 70000; k++ {
		want := firstIndex(0xfff0) + uint64(k)
		got := unwrap(h, uint16(want))
		if got != want {
			t.Fatalf("k=%d: unwrap=%x want %x", k, got, want)
		}
		h = got
	}
	// a late packet from just before a wrap
	if got := unwrap(firstIndex(0)+0x10002, 0xfffe); got != firstIndex(0)+0xfffe {
		t.Fatalf("late across wrap: %x", got)
	}
}

func TestSealOpen(t *testing.T) {
	key := callKey(testSecret, "c1", "n1", "n2")
	tx, _ := newSealer(key, 42)
	rx, _ := newSealer(key, 42)
	idx := firstIndex(100)
	f := sine(FrameSamples, 0, 0.5)
	pkt := tx.seal(idx, true, f)
	if len(pkt) != headerLen+FrameBytes+tagLen {
		t.Fatalf("packet %d bytes", len(pkt))
	}
	got, s, err := rx.open(pkt)
	if err != nil || got != idx || len(s) != FrameSamples || s[10] != f[10] {
		t.Fatalf("open: idx %x err %v", got, err)
	}
	if _, _, err := rx.open(pkt); err != errReplay {
		t.Fatalf("replay: %v", err)
	}
	bad := append([]byte(nil), tx.seal(idx+1, false, f)...)
	bad[20] ^= 1
	if _, _, err := rx.open(bad); err != errAuth {
		t.Fatalf("tampered: %v", err)
	}
	other, _ := newSealer(callKey("another-secret-entirely", "c1", "n1", "n2"), 42)
	if _, _, err := rx.open(other.seal(idx+2, false, f)); err != errAuth {
		t.Fatalf("wrong key: %v", err)
	}
	if _, _, err := rx.open(tx.seal(idx+3, false, f)); err != nil {
		t.Fatalf("next after bad ones: %v", err)
	}
	// out of order inside the window is fine, once
	if _, _, err := rx.open(tx.seal(idx+2, false, f)); err != nil {
		t.Fatalf("reordered: %v", err)
	}
	wrongSSRC, _ := newSealer(key, 43)
	if _, _, err := rx.open(wrongSSRC.seal(idx+4, false, f)); err != errSSRC {
		t.Fatalf("ssrc: %v", err)
	}
}

func TestVerifier(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	v := newVerifier(testSecret)
	m := Msg{T: MsgInvite, Call: "c", From: "a", To: "b", Addr: "192.0.2.1", Port: 1, SSRC: 7}
	m.Sign(testSecret, now)
	if _, err := v.parse(m.Marshal(), now); err != nil {
		t.Fatal(err)
	}
	if _, err := v.parse(m.Marshal(), now); err != errNonce {
		t.Fatalf("replay: %v", err)
	}
	m.Sign(testSecret, now.Add(-time.Minute))
	if _, err := v.parse(m.Marshal(), now); err != errStale {
		t.Fatalf("stale: %v", err)
	}
	m.Sign("wrong-secret", now)
	if _, err := v.parse(m.Marshal(), now); err != errMAC {
		t.Fatalf("mac: %v", err)
	}
	m.Sign(testSecret, now)
	m.Port = 2 // changed after signing
	if _, err := v.parse(m.Marshal(), now); err != errMAC {
		t.Fatalf("tampered: %v", err)
	}
}

func TestJitterLossAndReorder(t *testing.T) {
	j := NewJitter(40)
	r := rand.New(rand.NewSource(1))
	base := firstIndex(5)
	var pending []uint64
	sent := 0
	for k := 0; k < 3000; k++ { // one frame time per step: the sender's frame k, one pop
		idx := base + uint64(k)
		if r.Float64() >= 0.05 { // 5 % loss
			pending = append(pending, idx)
			if r.Float64() < 0.1 && len(pending) > 1 { // reorder neighbours
				n := len(pending)
				pending[n-1], pending[n-2] = pending[n-2], pending[n-1]
			}
		}
		if len(pending) >= 2 { // one frame of network delay
			j.Push(pending[0], sine(FrameSamples, k*FrameSamples, 0.3))
			pending = pending[1:]
			sent++
		}
		if k > 2 {
			if f := j.Pop(); len(f) < FrameSamples-1 || len(f) > FrameSamples+1 {
				t.Fatalf("frame of %d", len(f))
			}
		}
	}
	st := j.Stats()
	t.Logf("%+v (sent %d)", st, sent)
	if st.Frames < sent*9/10 || st.Missing > 3000/10 || st.Rebuffers != 0 || st.Overflow != 0 {
		t.Fatalf("stats %+v (sent %d)", st, sent)
	}
}

// TestJitterDrift runs 30 virtual minutes with the far clock 300 ppm off either way: the depth
// must stay near the target, with no overflow and no rebuffering.
func TestJitterDrift(t *testing.T) {
	for _, ppm := range []float64{300, -300} {
		j := NewJitter(40)
		base := firstIndex(9)
		sendPeriod := float64(FrameDur) * (1 - ppm/1e6) // the far end produces frames this often
		var tSend, tPlay float64
		k := 0
		maxDepth := 0
		for tPlay < float64(30*time.Minute) {
			if tSend <= tPlay {
				j.Push(base+uint64(k), make([]int16, FrameSamples))
				k++
				tSend += sendPeriod
				continue
			}
			f := j.Pop()
			tPlay += float64(len(f)) * float64(time.Second) / Rate
			j.mu.Lock()
			if d := int(j.highest + 1 - j.next); d > maxDepth && tPlay > float64(time.Minute) {
				maxDepth = d
			}
			j.mu.Unlock()
		}
		st := j.Stats()
		t.Logf("ppm %+v: %+v, max depth %d frames", ppm, st, maxDepth)
		if st.Overflow != 0 || st.Rebuffers != 0 || st.Missing > 5 || maxDepth > 4 {
			t.Fatalf("ppm %v: %+v max depth %d", ppm, st, maxDepth)
		}
		if (ppm > 0 && st.Dropped == 0) || (ppm < 0 && st.Inserted == 0) {
			t.Fatalf("ppm %v: no drift correction: %+v", ppm, st)
		}
	}
}

func TestGuardFadeAndHalfDuplex(t *testing.T) {
	var g Guard
	now := time.Unix(100, 0)
	up := sine(FrameSamples, 0, 0.5)
	g.Up(up, now)
	if up[5] != 0 && math.Abs(float64(up[5])) > 200 {
		t.Fatalf("no fade-in at the start: %d", up[5])
	}
	// after the fade, a quiet room while the far end talks is turned down
	for i := 0; i < 40; i++ {
		g.Up(sine(FrameSamples, 0, 0.01), now)
	}
	for i := 0; i < 5; i++ {
		g.Far(sine(FrameSamples, 0, 0.3), now)
	}
	quiet := sine(FrameSamples, 0, 0.01)
	g.Up(sine(FrameSamples, 0, 0.01), now)
	g.Up(quiet, now)
	if r := rms(quiet); r > 0.01*0.3 {
		t.Fatalf("uplink not ducked under far speech: rms %.4f", r)
	}
	if !g.FarSpokeWithin(now.Add(time.Second), 1500*time.Millisecond) || g.FarSpokeWithin(now.Add(2*time.Second), 1500*time.Millisecond) {
		t.Fatal("far speech window")
	}
	// loud local speech goes through even while the far end talks
	loud := sine(FrameSamples, 0, 0.5)
	g.Up(sine(FrameSamples, 0, 0.5), now)
	g.Up(loud, now)
	if r := rms(loud); r < 0.3 {
		t.Fatalf("local speech ducked: %.3f", r)
	}
}

func TestVoice(t *testing.T) {
	peers := []Peer{{"b", "Спальня"}, {"c", "Кабинет"}, {"d", "Детская"}, {"e", "Детская 2"}}
	for text, want := range map[string]string{
		"Соедини со спальней":         "b",
		"позвони в кабинет":           "c",
		"Алиса, свяжи меня с детской": "d",
		"набери детскую 2":            "e",
		"соедини":                     "",
		"сколько времени":             "",
		"включи свет в спальне":       "",
	} {
		p, ok := MatchCall(text, peers)
		if (want == "") == ok || p.ID != want {
			t.Errorf("%q: %v %v, want %q", text, p, ok, want)
		}
	}
	for _, s := range []string{"заверши звонок", "Алиса, положи трубку", "отбой"} {
		if !MatchHangup(s) {
			t.Errorf("hangup %q", s)
		}
	}
	if MatchHangup("какая погода") {
		t.Error("hangup false positive")
	}
	for text, want := range map[string][2]bool{
		"да": {true, false}, "Да, давай!": {true, false}, "нет": {false, true},
		"не сейчас": {false, true}, "что?": {false, false}, "да нет": {false, false},
	} {
		y, n := MatchAnswer(text)
		if y != want[0] || n != want[1] {
			t.Errorf("answer %q: %v %v", text, y, n)
		}
	}
}

// ---- two managers over loopback UDP, signalling handed across directly ----

type pair struct {
	a, b       *Manager
	mu         sync.Mutex
	statesA    []Status
	statesB    []Status
	dropToB    bool
	policyB    string
	autoAnswer bool
}

func freePort(t *testing.T) int {
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).Port
}

func newPair(t *testing.T, mod func(*Options)) *pair {
	p := &pair{}
	mk := func(id string, states *[]Status, peer **Manager) *Manager {
		o := Options{ID: id, Name: "room " + id, Secret: testSecret, Port: freePort(t), JitterMs: 40,
			RingTimeout: time.Second, MediaTimeout: time.Second,
			LocalIP: func() string { return "127.0.0.1" },
			Send: func(to string, b []byte) {
				p.mu.Lock()
				drop := to == "b" && p.dropToB
				p.mu.Unlock()
				if !drop {
					go (*peer).Handle(b)
				}
			},
			OnChange: func(s Status) {
				p.mu.Lock()
				*states = append(*states, s)
				p.mu.Unlock()
			},
		}
		if id == "b" {
			o.Policy = func(string) string {
				p.mu.Lock()
				defer p.mu.Unlock()
				return p.policyB
			}
		}
		if mod != nil {
			mod(&o)
		}
		return NewManager(o)
	}
	p.a = mk("a", &p.statesA, &p.b)
	p.b = mk("b", &p.statesB, &p.a)
	t.Cleanup(func() { p.a.Hangup("test end"); p.b.Hangup("test end") })
	return p
}

func waitState(t *testing.T, m *Manager, want State) Status {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if s := m.Status(); s.State == want {
			return s
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s: state %v, want %v", m.o.ID, m.Status(), want)
	return Status{}
}

func TestCallAnswerTalkHangup(t *testing.T) {
	p := newPair(t, nil)
	if err := p.a.Call("b", "room b"); err != nil {
		t.Fatal(err)
	}
	waitState(t, p.a, Calling)
	s := waitState(t, p.b, Ringing)
	if s.Peer != "a" || s.PeerName != "room a" || !s.Incoming {
		t.Fatalf("ringing status %+v", s)
	}
	if err := p.b.Answer(); err != nil {
		t.Fatal(err)
	}
	waitState(t, p.a, InCall)
	waitState(t, p.b, InCall)
	// audio both ways: 1 s of a sine, then look for it on the far side
	got := map[string]float64{}
	var wg sync.WaitGroup
	for _, m := range []*Manager{p.a, p.b} {
		wg.Add(1)
		go func(m *Manager) {
			defer wg.Done()
			for k := 0; k < 50; k++ {
				m.Uplink(sine(FrameSamples, k*FrameSamples, 0.4))
				time.Sleep(FrameDur)
			}
		}(m)
	}
	for _, m := range []*Manager{p.a, p.b} {
		wg.Add(1)
		go func(m *Manager) {
			defer wg.Done()
			best := 0.0
			for k := 0; k < 50; k++ {
				time.Sleep(FrameDur)
				if r := rms(m.Downlink()); r > best {
					best = r
				}
			}
			p.mu.Lock()
			got[m.o.ID] = best
			p.mu.Unlock()
		}(m)
	}
	wg.Wait()
	for id, r := range got {
		if r < 0.2 {
			t.Errorf("%s heard rms %.3f", id, r)
		}
	}
	p.a.Hangup("done")
	waitState(t, p.a, Idle)
	if s := waitState(t, p.b, Idle); s.Ended != "hung up by the peer" {
		t.Fatalf("b ended: %q", s.Ended)
	}
	if p.a.Downlink() != nil {
		t.Fatal("downlink after hangup")
	}
}

func TestDeclineBusyAndPolicy(t *testing.T) {
	p := newPair(t, nil)
	p.a.Call("b", "")
	waitState(t, p.b, Ringing)
	p.b.Hangup("declined by the user")
	if s := waitState(t, p.a, Idle); s.Ended != "rejected: declined" {
		t.Fatalf("a ended %q", s.Ended)
	}
	waitState(t, p.b, Idle)

	p.mu.Lock()
	p.policyB = ReasonOff
	p.mu.Unlock()
	p.a.Call("b", "")
	if s := waitState(t, p.a, Idle); s.Ended != "rejected: off" {
		t.Fatalf("a ended %q", s.Ended)
	}
	if p.b.Status().State != Idle {
		t.Fatal("b rang despite the policy")
	}
	if err := p.a.Call("a", ""); err != ErrSelfCall {
		t.Fatal(err)
	}
}

func TestRingTimeoutAndAutoAnswer(t *testing.T) {
	p := newPair(t, nil)
	p.a.Call("b", "")
	waitState(t, p.b, Ringing)
	time.Sleep(1200 * time.Millisecond)
	if s := waitState(t, p.a, Idle); s.Ended != "no answer" {
		t.Fatalf("a: %q", s.Ended)
	}
	if s := waitState(t, p.b, Idle); s.Ended != "missed" {
		t.Fatalf("b: %q", s.Ended)
	}

	q := newPair(t, func(o *Options) { o.AutoAnswer = o.ID == "b" })
	q.a.Call("b", "")
	waitState(t, q.a, InCall)
	waitState(t, q.b, InCall)
}

func TestMediaTimeout(t *testing.T) {
	p := newPair(t, nil)
	p.a.Call("b", "")
	waitState(t, p.b, Ringing)
	p.b.Answer()
	waitState(t, p.a, InCall)
	// nobody sends audio: both give up after MediaTimeout, without a bye
	p.mu.Lock()
	p.dropToB = true
	p.mu.Unlock()
	time.Sleep(1600 * time.Millisecond)
	if s := waitState(t, p.a, Idle); s.Ended != "no audio from the peer" {
		t.Fatalf("a: %q", s.Ended)
	}
	waitState(t, p.b, Idle)
}

func TestCrossedCalls(t *testing.T) {
	p := newPair(t, func(o *Options) {
		// hold each side's invite back a little, so both are calling when the other's arrives
		send := o.Send
		o.Send = func(to string, b []byte) {
			go func() { time.Sleep(50 * time.Millisecond); send(to, b) }()
		}
	})
	p.a.Call("b", "")
	p.b.Call("a", "")
	waitState(t, p.a, InCall)
	waitState(t, p.b, InCall)
}

func TestFramerAndUpsampler(t *testing.T) {
	var fr Framer
	var got [][]int16
	src := make([]byte, 2048*3+100) // mic-sized blocks of 64 ms
	for i := range src {
		src[i] = byte(i)
	}
	for i := 0; i < len(src); i += 2048 {
		end := i + 2048
		if end > len(src) {
			end = len(src)
		}
		fr.Push(src[i:end], func(f []int16) { got = append(got, f) })
	}
	if len(got) != len(src)/FrameBytes || len(got[0]) != FrameSamples {
		t.Fatalf("%d frames", len(got))
	}
	if got[1][0] != int16(uint16(src[FrameBytes])|uint16(src[FrameBytes+1])<<8) {
		t.Fatal("little-endian")
	}
	var up Upsampler3
	b := up.Bytes([]int16{300, 600})
	if len(b) != 12 {
		t.Fatalf("%d bytes", len(b))
	}
	s := func(i int) int16 { return int16(uint16(b[2*i]) | uint16(b[2*i+1])<<8) }
	if s(0) != 100 || s(2) != 300 || s(3) != 400 || s(5) != 600 {
		t.Fatalf("interpolation %d %d %d %d", s(0), s(2), s(3), s(5))
	}
}
