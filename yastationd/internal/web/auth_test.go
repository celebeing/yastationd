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
	secret := []byte("0123456789abcdef0123456789abcdef")
	tk := Tokens{TTL: time.Hour}
	tk.SetKey(TokenKey(secret, "admin", ""))
	a := tk.New()
	if !tk.Valid(a) || tk.Valid("nope") || tk.Valid("") || tk.Valid(a[:len(a)-1]+"A") {
		t.Fatal("tokens")
	}
	again := Tokens{TTL: time.Hour} // a restart: same secret and login, nothing else kept
	again.SetKey(TokenKey(secret, "admin", ""))
	if !again.Valid(a) {
		t.Fatal("token lost on a restart")
	}
	tk.SetKey(TokenKey(secret, "admin", HashPassword("new")))
	if tk.Valid(a) {
		t.Fatal("a password change must revoke")
	}
	old := Tokens{TTL: -time.Second}
	old.SetKey(TokenKey(secret, "admin", ""))
	if old.Valid(old.New()) {
		t.Fatal("expired")
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
