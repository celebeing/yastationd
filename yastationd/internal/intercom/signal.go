package intercom

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// Signalling messages between two daemons. The transport (MQTT in the daemon) only carries them;
// every message is signed with the shared secret, carries a fresh nonce and a timestamp, and is
// rejected when the signature is bad, the clock is far off or the nonce was seen before.

// Message types.
const (
	MsgInvite = "invite"
	MsgAccept = "accept"
	MsgReject = "reject"
	MsgBye    = "bye"
)

// Reject reasons.
const (
	ReasonBusy     = "busy"
	ReasonDeclined = "declined"
	ReasonTimeout  = "timeout"
	ReasonOff      = "off"
)

// Msg is one signalling message. To is the addressee's device id (also the topic it travels on).
type Msg struct {
	T      string `json:"t"`
	Call   string `json:"call"`
	From   string `json:"from"`
	To     string `json:"to"`
	Name   string `json:"name,omitempty"` // the sender's room name (shown/spoken on the callee)
	TS     int64  `json:"ts"`             // unix ms
	Nonce  string `json:"nonce"`
	Addr   string `json:"addr,omitempty"` // media endpoint (invite, accept)
	Port   int    `json:"port,omitempty"`
	SSRC   uint32 `json:"ssrc,omitempty"`
	Reason string `json:"reason,omitempty"`
	MAC    string `json:"mac,omitempty"`
}

func (m Msg) mac(secret string) string {
	m.MAC = ""
	b, _ := json.Marshal(m) // struct order: deterministic
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(b)
	return base64.RawStdEncoding.EncodeToString(h.Sum(nil))
}

// Sign fills the timestamp, a fresh nonce and the MAC.
func (m *Msg) Sign(secret string, now time.Time) {
	m.TS = now.UnixMilli()
	m.Nonce = randHex(12)
	m.MAC = m.mac(secret)
}

// Marshal is the wire form.
func (m Msg) Marshal() []byte {
	b, _ := json.Marshal(m)
	return b
}

const (
	maxClockSkew = 30 * time.Second
	maxMsgBytes  = 2048
)

var (
	errMAC   = errors.New("bad signature")
	errStale = errors.New("timestamp too far off (clock not synced?)")
	errNonce = errors.New("nonce seen before")
)

// verifier checks incoming messages; it remembers nonces for twice the allowed skew.
type verifier struct {
	secret string
	mu     sync.Mutex
	seen   map[string]time.Time
}

func newVerifier(secret string) *verifier {
	return &verifier{secret: secret, seen: map[string]time.Time{}}
}

func (v *verifier) parse(b []byte, now time.Time) (Msg, error) {
	var m Msg
	if len(b) > maxMsgBytes {
		return m, errors.New("message too long")
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, err
	}
	if !hmac.Equal([]byte(m.MAC), []byte(m.mac(v.secret))) {
		return m, errMAC
	}
	ts := time.UnixMilli(m.TS)
	if d := now.Sub(ts); d > maxClockSkew || d < -maxClockSkew {
		return m, errStale
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	for n, t := range v.seen {
		if now.Sub(t) > 2*maxClockSkew {
			delete(v.seen, n)
		}
	}
	if _, ok := v.seen[m.Nonce]; ok || m.Nonce == "" {
		return m, errNonce
	}
	v.seen[m.Nonce] = now
	return m, nil
}

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func randSSRC(not uint32) uint32 {
	for {
		b := make([]byte, 4)
		rand.Read(b)
		s := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
		if s != 0 && s != not {
			return s
		}
	}
}
