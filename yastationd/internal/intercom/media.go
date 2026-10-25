package intercom

import (
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// session is the media of one established call: our packets to the peer, the peer's into the
// jitter buffer. Datagrams from anywhere but the peer's address, with another SSRC, failing
// authentication or replayed are dropped (and counted).
type session struct {
	conn *net.UDPConn
	peer *net.UDPAddr
	tx   *sealer
	rx   *sealer
	jb   *Jitter

	mu    sync.Mutex // tx side
	idx   uint64
	first bool

	lastRx atomic.Int64 // unix nanos of the last good packet
	bad    atomic.Int64
	sent   atomic.Int64
	start  time.Time
	done   chan struct{}
}

func listenUDP(port int) (*net.UDPConn, error) {
	return net.ListenUDP("udp4", &net.UDPAddr{Port: port})
}

func newSession(conn *net.UDPConn, peerAddr string, peerPort int, key []byte, mySSRC, peerSSRC uint32, jitterMs int) (*session, error) {
	peer, err := net.ResolveUDPAddr("udp4", net.JoinHostPort(peerAddr, strconv.Itoa(peerPort)))
	if err != nil {
		return nil, err
	}
	tx, err := newSealer(key, mySSRC)
	if err != nil {
		return nil, err
	}
	rx, err := newSealer(key, peerSSRC)
	if err != nil {
		return nil, err
	}
	// The first sequence number is random but below half the space, so a receiver that missed
	// the very first packets still extends the numbers the way we do.
	seq := uint16(randSSRC(0) & 0x7fff)
	s := &session{conn: conn, peer: peer, tx: tx, rx: rx, jb: NewJitter(jitterMs),
		idx: firstIndex(seq), first: true, start: time.Now(), done: make(chan struct{})}
	go s.recv()
	return s, nil
}

func (s *session) recv() {
	defer close(s.done)
	buf := make([]byte, 2048)
	for {
		n, from, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			return // closed
		}
		if !from.IP.Equal(s.peer.IP) || from.Port != s.peer.Port {
			s.bad.Add(1)
			continue
		}
		idx, samples, err := s.rx.open(buf[:n])
		if err != nil {
			s.bad.Add(1)
			continue
		}
		s.lastRx.Store(time.Now().UnixNano())
		s.jb.Push(idx, samples)
	}
}

// send seals and sends one uplink frame.
func (s *session) send(frame []int16) error {
	s.mu.Lock()
	pkt := s.tx.seal(s.idx, s.first, frame)
	s.idx++
	s.first = false
	s.mu.Unlock()
	_, err := s.conn.WriteToUDP(pkt, s.peer)
	if err == nil {
		s.sent.Add(1)
	}
	return err
}

// heard is when the last good packet arrived (the start of the session before any).
func (s *session) heard() time.Time {
	if n := s.lastRx.Load(); n != 0 {
		return time.Unix(0, n)
	}
	return s.start
}

func (s *session) close() {
	s.conn.Close()
	<-s.done
}
