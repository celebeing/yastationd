package hw

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A fake aplay on PATH records its arguments and what it was fed.
func TestLiveOutFeedsAplay(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\necho \"$@\" > " + dir + "/args\ncat > " + dir + "/pcm\n"
	if err := os.WriteFile(filepath.Join(dir, "aplay"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	o, err := StartLiveOut("default", 48000)
	if err != nil {
		t.Fatal(err)
	}
	frame := make([]byte, 1920)
	for i := 0; i < 10; i++ {
		if _, err := o.Write(frame); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	o.Close()
	if time.Since(start) > 400*time.Millisecond {
		t.Fatal("Close waited for the SIGTERM fallback although aplay exits on EOF")
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args"))
	for _, want := range []string{"-r 48000", "-c 1", "--buffer-time=60000", "-t raw"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("aplay args %q lack %q", args, want)
		}
	}
	if st, _ := os.Stat(filepath.Join(dir, "pcm")); st == nil || st.Size() != 19200 {
		t.Fatalf("aplay got %v", st)
	}
}
