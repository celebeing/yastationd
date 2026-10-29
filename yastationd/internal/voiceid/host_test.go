package voiceid

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeHost is a shell stand-in for `spotter-host voice`: it counts the bytes it is fed and answers
// a voiceprint request with "VOICE <tag> <secs> 1 2 3" (short below 32000 bytes, i.e. 1 s).
const fakeHost = `#!/bin/sh
echo "READY test_tag"
n=0
while read -r c arg; do
  case $c in
  a) head -c "$arg" >/dev/null; n=$((n + arg)) ;;
  r) n=0 ;;
  v) if [ $n -lt 32000 ]; then echo "VOICE $arg 0.50 short"; else echo "VOICE $arg $((n / 32000)).00 1 2 3"; fi ;;
  esac
done
`

func TestHostProtocol(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "spotter-host")
	if err := os.WriteFile(bin, []byte(fakeHost), 0o755); err != nil {
		t.Fatal(err)
	}
	h := &Host{Bin: bin, Dir: "/nowhere", Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	u, err := h.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if h.Tag() != "test_tag" {
		t.Errorf("tag %q", h.Tag())
	}
	u.Feed(make([]byte, 16000))
	if p := u.Print(time.Second); p.Err != nil || p.Emb != nil {
		t.Fatalf("half a second should be short: %+v", p)
	}
	for i := 0; i < 4; i++ {
		u.Feed(make([]byte, 16000))
	}
	p := u.Print(time.Second)
	if p.Err != nil || len(p.Emb) != 3 || p.Emb[2] != 3 || p.Secs != 2 {
		t.Fatalf("print %+v", p)
	}
	// a new utterance resets; the old one is dead
	u2, _ := h.Begin()
	u.Feed(make([]byte, 64000))
	if p := u.Print(time.Second); p.Err == nil {
		t.Error("a superseded utterance still prints")
	}
	if p := u2.Print(time.Second); p.Emb != nil {
		t.Errorf("the new utterance got the old one's audio: %+v", p)
	}
	// the child dies: the next utterance starts it again
	h.Stop()
	time.Sleep(100 * time.Millisecond)
	u3, err := h.Begin()
	if err != nil {
		t.Fatal(err)
	}
	u3.Feed(make([]byte, 40000))
	if p := u3.Print(time.Second); p.Err != nil || p.Emb == nil {
		t.Fatalf("after a restart: %+v", p)
	}
}
