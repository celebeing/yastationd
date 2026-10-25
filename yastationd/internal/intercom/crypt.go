package intercom

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
)

// Media keys: one per call, derived from the shared secret and both sides' nonces, so a recorded
// call can't be replayed into another one. Both directions use the same key; their GCM nonces
// differ by the SSRC (the two sides pick different ones).

func callKey(secret, call, callerNonce, calleeNonce string) []byte {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte("yastation-intercom/v1|" + call + "|" + callerNonce + "|" + calleeNonce))
	return m.Sum(nil)[:16]
}

// sealer protects one direction of a call: the sender's packets (seal), or the peer's (open).
type sealer struct {
	aead cipher.AEAD
	ssrc uint32

	// open side: the highest extended index accepted so far and a bitmap of the 64 below it
	highest uint64
	seen    uint64
	started bool
}

func newSealer(key []byte, ssrc uint32) (*sealer, error) {
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(b)
	if err != nil {
		return nil, err
	}
	return &sealer{aead: g, ssrc: ssrc}, nil
}

func (s *sealer) nonce(idx uint64) []byte {
	n := make([]byte, 12)
	binary.BigEndian.PutUint32(n, s.ssrc)
	binary.BigEndian.PutUint64(n[4:], idx)
	return n
}

// seal builds the packet for frame number idx (the sender's own extended counter).
func (s *sealer) seal(idx uint64, marker bool, samples []int16) []byte {
	pkt := make([]byte, headerLen, headerLen+len(samples)*2+tagLen)
	header{marker: marker, seq: uint16(idx), ts: uint32(idx) * FrameSamples, ssrc: s.ssrc}.put(pkt)
	pay := make([]byte, len(samples)*2)
	putL16(pay, samples)
	return s.aead.Seal(pkt, s.nonce(idx), pay, pkt[:headerLen])
}

var (
	errAuth   = errors.New("intercom: packet failed authentication")
	errReplay = errors.New("intercom: replayed or too old packet")
	errSSRC   = errors.New("intercom: packet from another stream")
)

const replayWindow = 64

// open checks and decrypts a peer packet; it returns the packet's extended index.
func (s *sealer) open(pkt []byte) (uint64, []int16, error) {
	h, err := parseHeader(pkt)
	if err != nil {
		return 0, nil, err
	}
	if h.ssrc != s.ssrc {
		return 0, nil, errSSRC
	}
	idx := firstIndex(h.seq)
	if s.started {
		idx = unwrap(s.highest, h.seq)
		if idx+replayWindow <= s.highest {
			return 0, nil, errReplay
		}
		if idx <= s.highest && s.seen&(1<<(s.highest-idx)) != 0 {
			return 0, nil, errReplay
		}
	}
	pay, err := s.aead.Open(nil, s.nonce(idx), pkt[headerLen:], pkt[:headerLen])
	if err != nil {
		return 0, nil, errAuth
	}
	// Only an authentic packet moves the window.
	switch {
	case !s.started:
		s.started, s.highest, s.seen = true, idx, 1
	case idx > s.highest:
		if d := idx - s.highest; d < replayWindow {
			s.seen = s.seen<<d | 1
		} else {
			s.seen = 1
		}
		s.highest = idx
	default:
		s.seen |= 1 << (s.highest - idx)
	}
	return idx, getL16(pay), nil
}
