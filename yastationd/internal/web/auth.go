// Package web is the speaker's HTTP server plumbing (docs/web-ui.md): password hashing, bearer
// tokens, slowing down failed logins, CORS. The app's handlers live in internal/app.
package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultUser and DefaultPassword are the login of a speaker whose config sets none — written
// and documented on purpose (docs/web-ui.md, "Login").
const (
	DefaultUser     = "admin"
	DefaultPassword = "yastation"
)

const (
	hashIterations = 60000 // ~0.3 s on a Mini 3: once per login, not per request
	hashPrefix     = "pbkdf2-sha256"
)

// pbkdf2 is RFC 8018 PBKDF2 with HMAC-SHA256, one output block (32 bytes). (Go 1.22 has no
// crypto/pbkdf2 yet.)
func pbkdf2(password, salt []byte, iter int) []byte {
	mac := hmac.New(sha256.New, password)
	mac.Write(salt)
	mac.Write([]byte{0, 0, 0, 1})
	u := mac.Sum(nil)
	out := append([]byte(nil), u...)
	for i := 1; i < iter; i++ {
		mac.Reset()
		mac.Write(u)
		u = mac.Sum(u[:0])
		for j := range out {
			out[j] ^= u[j]
		}
	}
	return out
}

// HashPassword returns "pbkdf2-sha256$<iter>$<salt>$<hash>" (base64, no padding).
func HashPassword(password string) string {
	salt := make([]byte, 16)
	rand.Read(salt)
	h := pbkdf2([]byte(password), salt, hashIterations)
	enc := base64.RawStdEncoding
	return fmt.Sprintf("%s$%d$%s$%s", hashPrefix, hashIterations, enc.EncodeToString(salt), enc.EncodeToString(h))
}

// CheckPassword compares password with a stored hash; an empty hash means the default password.
func CheckPassword(stored, password string) bool {
	if stored == "" {
		return subtle.ConstantTimeCompare([]byte(password), []byte(DefaultPassword)) == 1
	}
	f := strings.Split(stored, "$")
	if len(f) != 4 || f[0] != hashPrefix {
		return false
	}
	iter, err := strconv.Atoi(f[1])
	if err != nil || iter < 1 || iter > 10000000 {
		return false
	}
	enc := base64.RawStdEncoding
	salt, err1 := enc.DecodeString(f[2])
	want, err2 := enc.DecodeString(f[3])
	if err1 != nil || err2 != nil {
		return false
	}
	return subtle.ConstantTimeCompare(pbkdf2([]byte(password), salt, iter), want) == 1
}

// ValidHash: s is a hash HashPassword could have written (for the config parser).
func ValidHash(s string) bool {
	f := strings.Split(s, "$")
	return len(f) == 4 && f[0] == hashPrefix
}

// Tokens are bearer tokens, in memory only (a restart logs everyone out).
type Tokens struct {
	TTL time.Duration
	mu  sync.Mutex
	m   map[string]time.Time
}

// New issues a token.
func (t *Tokens) New() string {
	b := make([]byte, 24)
	rand.Read(b)
	tok := base64.RawURLEncoding.EncodeToString(b)
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.m == nil {
		t.m = map[string]time.Time{}
	}
	now := time.Now()
	for k, exp := range t.m {
		if now.After(exp) {
			delete(t.m, k)
		}
	}
	t.m[tok] = now.Add(t.TTL)
	return tok
}

// Valid reports whether tok is a live token.
func (t *Tokens) Valid(tok string) bool {
	if tok == "" {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	exp, ok := t.m[tok]
	return ok && time.Now().Before(exp)
}

// RevokeAll logs everyone out (after a password change).
func (t *Tokens) RevokeAll() {
	t.mu.Lock()
	t.m = nil
	t.mu.Unlock()
}

// Throttle slows down repeated failed logins from one client: each failure in a row doubles
// the wait before the next attempt is even checked (1 s, 2 s, 4 s … up to a minute).
type Throttle struct {
	mu sync.Mutex
	m  map[string]throttleState
}

type throttleState struct {
	fails int
	until time.Time
}

// Wait is how long client must wait before its next attempt (0 = go ahead).
func (t *Throttle) Wait(client string) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	if d := time.Until(t.m[client].until); d > 0 {
		return d
	}
	return 0
}

// Result records an attempt.
func (t *Throttle) Result(client string, ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.m == nil {
		t.m = map[string]throttleState{}
	}
	if ok {
		delete(t.m, client)
		return
	}
	s := t.m[client]
	s.fails++
	d := time.Second << min(s.fails-1, 6)
	s.until = time.Now().Add(d)
	t.m[client] = s
}
