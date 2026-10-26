package web

import (
	"encoding/hex"
	"testing"
	"time"
)

// RFC 7914 §11 test vector for PBKDF2-HMAC-SHA256 (first 32 bytes).
func TestPBKDF2Vector(t *testing.T) {
	got := hex.EncodeToString(pbkdf2([]byte("passwd"), []byte("salt"), 1))
	if got != "55ac046e56e3089fec1691c22544b605f94185216dde0465e68b9d57c20dacbc" {
		t.Fatal(got)
	}
}

func TestPasswords(t *testing.T) {
	h := HashPassword("secret")
	if !ValidHash(h) || !CheckPassword(h, "secret") || CheckPassword(h, "Secret") {
		t.Fatal("hash round trip")
	}
	if !CheckPassword("", DefaultPassword) || CheckPassword("", "x") {
		t.Fatal("default password")
	}
}

func TestTokensAndThrottle(t *testing.T) {
	tk := Tokens{TTL: time.Hour}
	a := tk.New()
	if !tk.Valid(a) || tk.Valid("nope") || tk.Valid("") {
		t.Fatal("tokens")
	}
	tk.RevokeAll()
	if tk.Valid(a) {
		t.Fatal("revoked")
	}
	var th Throttle
	th.Result("c", false)
	if th.Wait("c") <= 0 || th.Wait("d") != 0 {
		t.Fatal("throttle")
	}
	th.Result("c", true)
	if th.Wait("c") != 0 {
		t.Fatal("reset on success")
	}
}
