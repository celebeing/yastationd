package logship

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTest(t *testing.T) (*Shipper, string) {
	dir := t.TempDir()
	s := &Shipper{Addr: "x", Host: "unit", Spool: filepath.Join(dir, "spool"), SpoolMax: 4096,
		PosFile: filepath.Join(dir, "pos"), Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Sources: []Source{{Path: filepath.Join(dir, "d.log"), App: "yastationd"}, {Path: filepath.Join(dir, "messages"), App: "syslog"}}}
	s.init()
	return s, dir
}

func appendFile(t *testing.T, p, s string) {
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(s)
	f.Close()
}

func TestFormat(t *testing.T) {
	s, _ := newTest(t)
	now := time.Date(2026, 10, 26, 13, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	got := s.format("yastationd", `time=2026-10-26T12:59:58.5Z level=WARN msg="mic: capture ended"`)
	if want := `<28>1 2026-10-26T12:59:58.5Z unit yastationd - - - time=2026-10-26T12:59:58.5Z level=WARN msg="mic: capture ended"`; got != want {
		t.Errorf("slog line:\n got %s\nwant %s", got, want)
	}
	got = s.format("syslog", "Oct 26 12:58:01 Yandex-x kern.warn kernel: [ 12.3] dhd: tx timeout")
	if !strings.HasPrefix(got, "<4>1 2026-10-26T12:58:01") || !strings.HasSuffix(got, " unit kernel - - - [ 12.3] dhd: tx timeout") {
		t.Errorf("syslog line: %s", got)
	}
	// A boot line stamped 1970 gets the time it was read.
	if got = s.format("syslog", "Jan  1 00:00:03 h syslog.info syslogd started: BusyBox"); !strings.Contains(got, "2026-10-26T13:00:00Z") {
		t.Errorf("stale stamp: %s", got)
	}
}

func TestTailFollowsTruncateAndRotation(t *testing.T) {
	s, dir := newTest(t)
	d := filepath.Join(dir, "d.log")
	appendFile(t, d, "a\nb\npart")
	s.readSources()
	if len(s.pending) != 2 {
		t.Fatalf("pending %d (a partial line waits)", len(s.pending))
	}
	appendFile(t, d, "ial\n")
	s.readSources()
	if len(s.pending) != 3 || !strings.HasSuffix(s.pending[2], " partial") {
		t.Fatalf("pending %q", s.pending)
	}
	os.WriteFile(d, []byte("c\n"), 0o644) // copy-truncate: shorter than the offset
	s.readSources()
	if !strings.HasSuffix(s.pending[3], " c") {
		t.Fatalf("after truncate: %q", s.pending)
	}
	// A restart resumes from the saved position.
	s2 := &Shipper{PosFile: s.PosFile, Sources: s.Sources, SpoolMax: 4096, Log: s.Log}
	s2.init()
	s2.readSources()
	if len(s2.pending) != 0 {
		t.Fatalf("resumed run re-read %q", s2.pending)
	}
}

type fakeConn struct {
	net.Conn
	got  *strings.Builder
	fail bool
}

func (c *fakeConn) Write(b []byte) (int, error) {
	if c.fail {
		return 0, errors.New("broken pipe")
	}
	c.got.Write(b)
	return len(b), nil
}
func (c *fakeConn) SetWriteDeadline(time.Time) error { return nil }
func (c *fakeConn) Close() error                     { return nil }

func TestSpoolWhileDownThenSend(t *testing.T) {
	s, dir := newTest(t)
	now := time.Now()
	s.now = func() time.Time { return now }
	up := false
	var got strings.Builder
	s.dial = func(string) (net.Conn, error) {
		if !up {
			return nil, errors.New("no route to host")
		}
		return &fakeConn{got: &got}, nil
	}
	d := filepath.Join(dir, "d.log")
	appendFile(t, d, "one\ntwo\n")
	s.readSources()
	s.ship()
	if len(s.pending) != 2 {
		t.Fatal("lines kept while down")
	}
	s.Flush()
	if b, _ := os.ReadFile(s.Spool); strings.Count(string(b), "\n") != 2 || len(s.pending) != 0 {
		t.Fatalf("spool %q pending %d", b, len(s.pending))
	}
	appendFile(t, d, "three\n")
	s.readSources()
	up = true
	now = now.Add(retryEvery)
	s.ship()
	lines := []string{}
	sc := bufio.NewScanner(strings.NewReader(got.String()))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		lines = append(lines, f[len(f)-1])
	}
	if strings.Join(lines, ",") != "one,two,three" {
		t.Fatalf("sent %v", lines)
	}
	if _, err := os.Stat(s.Spool); err == nil || len(s.pending) != 0 {
		t.Fatal("spool and pending cleared after sending")
	}
}

func TestSpoolCap(t *testing.T) {
	s, _ := newTest(t)
	s.SpoolMax = 100
	for i := 0; i < 30; i++ {
		s.add(strings.Repeat("x", 9))
	}
	s.spoolPending()
	b, _ := os.ReadFile(s.Spool)
	if len(b) > 100 || len(b) == 0 || b[len(b)-1] != '\n' {
		t.Fatalf("spool %d bytes", len(b))
	}
}

// A large spool goes out in chunks of whole lines, and a connection lost midway resumes where it was.
func TestLargeSpoolResumes(t *testing.T) {
	s, _ := newTest(t)
	s.SpoolMax = 8 * sentKeep
	line := strings.Repeat("x", 99) + "\n"
	total := 3 * sentKeep / len(line)
	if err := os.WriteFile(s.Spool, []byte(strings.Repeat(line, total)), 0o644); err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	conn := &recConn{w: &got}
	s.dial = func(string) (net.Conn, error) { return conn, nil }
	s.unacked = func(net.Conn) int { return 0 }
	s.ship()
	if got.Len() == 0 || got.Len() > sentKeep || got.Len()%len(line) != 0 {
		t.Fatalf("first chunk %d bytes", got.Len())
	}
	conn.fail = true
	s.ship() // the link breaks mid-spool
	conn.fail = false
	if s.conn != nil {
		t.Fatal("a failed send keeps the connection")
	}
	s.nextTry = s.now()
	for i := 0; i < 10; i++ {
		s.nextTry = s.now()
		s.ship()
	}
	if got.Len() != total*len(line) {
		t.Fatalf("sent %d of %d bytes", got.Len(), total*len(line))
	}
	if _, err := os.Stat(s.Spool); err == nil {
		t.Error("the sent spool is still there")
	}
}

// A dead link: writes succeed into the socket buffer, nothing is acknowledged. The lines come
// back and end up in the spool.
// An outage appends to the spool; only going over the cap rewrites it, and then it drops to half,
// so a long outage doesn't rewrite a full spool every minute.
func TestSpoolAppendsAndCompactsRarely(t *testing.T) {
	s, _ := newTest(t)
	s.SpoolMax = 1000
	inode := func() os.FileInfo {
		fi, err := os.Stat(s.Spool)
		if err != nil {
			t.Fatal(err)
		}
		return fi
	}
	s.pending = []string{strings.Repeat("a", 99)}
	s.spoolPending()
	first := inode()
	rewrites, n := 0, 0
	last := first
	for i := 0; i < 1000; i++ {
		s.pending = []string{fmt.Sprintf("%04d", i) + strings.Repeat("b", 95)}
		n++
		s.spoolPending()
		if now := inode(); !os.SameFile(last, now) {
			rewrites++
			last = now
		}
		if sz := inode().Size(); sz > 1000 {
			t.Fatalf("the spool grew to %d", sz)
		}
	}
	// 100 bytes a line, 5 lines kept after a rewrite, 5 more until the next
	if rewrites < 90 || rewrites > 210 {
		t.Errorf("%d rewrites for %d lines", rewrites, n)
	}
	b, _ := os.ReadFile(s.Spool)
	if !strings.Contains(string(b), "0999") || strings.Contains(string(b), "0900") {
		t.Errorf("not the newest lines: %q", b)
	}
	if strings.Count(string(b), "\n") < 1 || !strings.HasSuffix(string(b), "\n") {
		t.Errorf("broken lines: %q", b)
	}
}

func TestDeadLinkRequeuesUnacknowledged(t *testing.T) {
	s, dir := newTest(t)
	now := time.Now()
	s.now = func() time.Time { return now }
	var got strings.Builder
	s.dial = func(string) (net.Conn, error) { return &fakeConn{got: &got}, nil }
	acked := true
	s.unacked = func(net.Conn) int {
		if acked {
			return 0
		}
		return len("<30>1 ") // a few bytes inside the last line
	}
	d := filepath.Join(dir, "d.log")
	appendFile(t, d, "one\n")
	s.readSources()
	s.checkConn()
	s.ship()
	acked = false
	appendFile(t, d, "two\n")
	s.readSources()
	s.checkConn()
	s.ship() // "two" goes into the dead socket
	s.checkConn()
	now = now.Add(stuckAfter)
	s.checkConn()
	if s.conn != nil {
		t.Fatal("a connection acknowledging nothing is dropped")
	}
	if len(s.pending) != 1 || !strings.HasSuffix(s.pending[0], " two") {
		t.Fatalf("requeued %q", s.pending)
	}
	s.Flush()
	if b, _ := os.ReadFile(s.Spool); !strings.HasSuffix(strings.TrimSpace(string(b)), " two") {
		t.Fatalf("spool %q", b)
	}
}

// recConn collects what is written; fail makes writes fail.
type recConn struct {
	net.Conn
	w    *bytes.Buffer
	fail bool
}

func (c *recConn) Write(b []byte) (int, error) {
	if c.fail {
		return 0, errors.New("broken")
	}
	return c.w.Write(b)
}
func (c *recConn) Close() error                     { return nil }
func (c *recConn) SetWriteDeadline(time.Time) error { return nil }
