// Package logship sends the speaker's logs to a syslog server on the LAN, so a speaker that hangs
// and has to be power-cycled (its logs are on tmpfs) leaves its last lines somewhere else.
//
// It tails log files (the daemon's log, which carries the supervisor's lines too, and the system
// log with the kernel's) and sends each new line over TCP as RFC 5424 syslog, newline-framed, with
// the line's own time and zone, so a late delivery keeps its time. While the server cannot be
// reached the lines wait in memory, and once a minute they are appended to a spool file on flash,
// capped in size (the oldest go first); when the server is back the spool is sent first. Flash is
// written only while the network is down. Flush puts what is waiting on flash at once (before a
// deliberate reboot).
package logship

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Source is a log file to tail and the syslog app name its lines go out under.
type Source struct {
	Path string
	App  string
}

type Shipper struct {
	Addr     string // host:port of the syslog server (TCP)
	Host     string // the HOSTNAME field: the device id
	Sources  []Source
	Spool    string // spool file on flash
	SpoolMax int    // its cap in bytes (and of the lines kept in memory)
	PosFile  string // read positions, on tmpfs: a daemon restart resumes, a reboot starts over
	Log      *slog.Logger

	mu      sync.Mutex
	pending []string // formatted lines not yet sent nor spooled
	size    int
	pos     map[string]filePos
	conn    net.Conn
	nextTry time.Time
	down    bool // the last attempt failed (for logging only the changes)
	spooled time.Time
	sent    []byte    // the tail of what was written to conn: unacknowledged bytes come back from it
	stuckAt time.Time // when the socket's unacknowledged queue was first seen non-empty
	now     func() time.Time
	unacked func(net.Conn) int // bytes written but not yet acknowledged by the server
	dial    func(addr string) (net.Conn, error)
}

type filePos struct {
	Inode  uint64 `json:"inode"`
	Offset int64  `json:"offset"`
}

const (
	readEvery   = time.Second
	stuckAfter  = 20 * time.Second // unacknowledged data this long: the link is dead
	userTimeout = 20 * time.Second
	sentKeep    = 256 << 10
	spoolEvery  = time.Minute
	retryEvery  = 10 * time.Second
	sendWait    = 5 * time.Second
)

func (s *Shipper) init() {
	if s.now == nil {
		s.now = time.Now
	}
	if s.dial == nil {
		s.dial = func(addr string) (net.Conn, error) { return net.DialTimeout("tcp", addr, 3*time.Second) }
	}
	if s.SpoolMax <= 0 {
		s.SpoolMax = 1 << 20
	}
	if s.unacked == nil {
		s.unacked = socketUnacked
	}
	s.pos = map[string]filePos{}
	if b, err := os.ReadFile(s.PosFile); err == nil {
		json.Unmarshal(b, &s.pos)
	}
}

// Run tails and ships until stop.
func (s *Shipper) Run(stop <-chan struct{}) {
	s.init()
	s.spooled = s.now()
	t := time.NewTicker(readEvery)
	defer t.Stop()
	for {
		s.mu.Lock()
		s.readSources()
		s.checkConn()
		s.ship()
		if len(s.pending) > 0 && s.conn == nil && s.now().Sub(s.spooled) >= spoolEvery {
			s.spoolPending()
		}
		s.mu.Unlock()
		select {
		case <-stop:
			s.Flush()
			return
		case <-t.C:
		}
	}
}

// Flush appends the lines waiting in memory to the spool file and syncs it.
func (s *Shipper) Flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pos == nil { // never ran
		return
	}
	s.readSources()
	s.spoolPending()
}

// readSources appends every source's new lines to pending.
func (s *Shipper) readSources() {
	changed := false
	for _, src := range s.Sources {
		if s.readSource(src) {
			changed = true
		}
	}
	if changed && s.PosFile != "" {
		if b, err := json.Marshal(s.pos); err == nil {
			os.WriteFile(s.PosFile, b, 0o644)
		}
	}
}

func (s *Shipper) readSource(src Source) bool {
	f, err := os.Open(src.Path)
	if err != nil {
		return false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return false
	}
	var ino uint64
	if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		ino = uint64(sys.Ino)
	}
	p := s.pos[src.Path]
	if p.Inode != ino || st.Size() < p.Offset { // a new file, or one truncated (copy-truncate)
		p = filePos{Inode: ino}
	}
	if st.Size() == p.Offset {
		return false
	}
	if _, err := f.Seek(p.Offset, io.SeekStart); err != nil {
		return false
	}
	r := bufio.NewReaderSize(f, 64<<10)
	for {
		line, err := r.ReadString('\n')
		if err != nil { // a partial last line waits for its newline
			break
		}
		p.Offset += int64(len(line))
		if l := strings.TrimRight(line, "\r\n"); l != "" {
			s.add(s.format(src.App, l))
		}
	}
	s.pos[src.Path] = p
	return true
}

func (s *Shipper) add(line string) {
	s.pending = append(s.pending, line)
	s.size += len(line) + 1
	for s.size > s.SpoolMax && len(s.pending) > 1 {
		s.size -= len(s.pending[0]) + 1
		s.pending = s.pending[1:]
	}
}

var (
	slogTime  = regexp.MustCompile(`(?:^|\s)time=(\S+)`)
	slogLevel = regexp.MustCompile(`(?:^|\s)level=(\w+)`)
	// busybox syslogd: "Oct 26 13:52:00 host kern.warn kernel: [ 12.3] msg"
	sysLine = regexp.MustCompile(`^(\w{3} [ \d]\d \d\d:\d\d:\d\d) \S+ (\w+)\.(\w+) ([^:\[\s]+)(?:\[\d+\])?: ?(.*)$`)
)

var severities = map[string]int{"emerg": 0, "alert": 1, "crit": 2, "err": 3, "error": 3, "warn": 4,
	"warning": 4, "notice": 5, "info": 6, "debug": 7}

var facilities = map[string]int{"kern": 0, "user": 1, "mail": 2, "daemon": 3, "auth": 4, "syslog": 5,
	"lpr": 6, "news": 7, "uucp": 8, "cron": 9, "authpriv": 10, "ftp": 11, "local0": 16, "local1": 17,
	"local2": 18, "local3": 19, "local4": 20, "local5": 21, "local6": 22, "local7": 23}

// format turns a log line into an RFC 5424 message: its own time when the line carries one (slog's
// time=, syslogd's prefix), else now.
func (s *Shipper) format(app, line string) string {
	now := s.now()
	t, fac, sev, msg := now, 1, 6, line // user.info
	if m := sysLine.FindStringSubmatch(line); m != nil {
		if pt, err := time.ParseInLocation("Jan _2 15:04:05", m[1], time.Local); err == nil {
			pt = pt.AddDate(now.Year(), 0, 0)
			if pt.After(now.Add(24 * time.Hour)) { // a December line read in January
				pt = pt.AddDate(-1, 0, 0)
			}
			if pt.Year() >= 2026 && pt.Before(now.Add(time.Minute)) {
				t = pt
			}
		}
		if f, ok := facilities[m[2]]; ok {
			fac = f
		}
		if v, ok := severities[m[3]]; ok {
			sev = v
		}
		app, msg = m[4], m[5]
	} else {
		if m := slogTime.FindStringSubmatch(line); m != nil {
			if pt, err := time.Parse(time.RFC3339Nano, m[1]); err == nil {
				t = pt
			}
		}
		if m := slogLevel.FindStringSubmatch(line); m != nil {
			if v, ok := severities[strings.ToLower(m[1])]; ok {
				sev = v
			}
		}
		fac = 3 // daemon
	}
	return fmt.Sprintf("<%d>1 %s %s %s - - - %s", fac*8+sev, t.Format(time.RFC3339Nano), nilDash(s.Host), nilDash(app), msg)
}

func nilDash(v string) string {
	if v == "" {
		return "-"
	}
	return strings.ReplaceAll(v, " ", "_")
}

// ship sends the spool file, then the pending lines; on a failure they stay where they were.
func (s *Shipper) ship() {
	spool, _ := os.ReadFile(s.Spool)
	if len(spool) == 0 && len(s.pending) == 0 {
		return
	}
	if s.conn == nil {
		if s.now().Before(s.nextTry) {
			return
		}
		c, err := s.dial(s.Addr)
		if err == nil {
			setUserTimeout(c, userTimeout)
		}
		if err != nil {
			s.nextTry = s.now().Add(retryEvery)
			if !s.down {
				s.Log.Warn("logship: server unreachable, keeping the lines", "addr", s.Addr, "err", err)
				s.down = true
			}
			return
		}
		s.conn = c
		if s.down {
			s.Log.Info("logship: server back", "addr", s.Addr, "spooled_bytes", len(spool), "pending", len(s.pending))
			s.down = false
		}
	}
	if len(spool) > 0 {
		if !s.write(spool) {
			return
		}
		os.Remove(s.Spool)
	}
	if len(s.pending) > 0 {
		var b bytes.Buffer
		for _, l := range s.pending {
			b.WriteString(l)
			b.WriteByte('\n')
		}
		if !s.write(b.Bytes()) {
			return
		}
		s.pending, s.size = nil, 0
	}
}

func (s *Shipper) write(b []byte) bool {
	s.conn.SetWriteDeadline(s.now().Add(sendWait))
	if _, err := s.conn.Write(b); err != nil {
		s.drop("send failed", err)
		return false
	}
	s.sent = append(s.sent, b...)
	if over := len(s.sent) - sentKeep; over > 0 {
		s.sent = append(s.sent[:0], s.sent[over:]...)
	}
	return true
}

// checkConn notices a connection whose data the server stopped acknowledging (a write into a
// dead link succeeds: it only fills the socket's buffer) and drops it, taking the
// unacknowledged lines back.
func (s *Shipper) checkConn() {
	if s.conn == nil {
		return
	}
	if s.unacked(s.conn) <= 0 {
		s.stuckAt = time.Time{}
		return
	}
	if s.stuckAt.IsZero() {
		s.stuckAt = s.now()
	} else if s.now().Sub(s.stuckAt) >= stuckAfter {
		s.drop("server not acknowledging", nil)
	}
}

// drop closes the connection and puts the lines the server has not acknowledged back in front
// of pending (some may arrive twice; none is lost).
func (s *Shipper) drop(why string, err error) {
	n := s.unacked(s.conn)
	s.conn.Close()
	s.conn = nil
	s.stuckAt = time.Time{}
	s.nextTry = s.now().Add(retryEvery)
	if n > len(s.sent) {
		n = len(s.sent)
	}
	var back []string
	if n > 0 {
		// from the start of the line the unacknowledged bytes begin in
		start := bytes.LastIndexByte(s.sent[:len(s.sent)-n], '\n') + 1
		for _, l := range strings.Split(strings.TrimRight(string(s.sent[start:]), "\n"), "\n") {
			if l != "" {
				back = append(back, l)
			}
		}
	}
	s.sent = nil
	if len(back) > 0 {
		old := s.pending
		s.pending, s.size = nil, 0
		for _, l := range append(back, old...) {
			s.add(l)
		}
	}
	s.Log.Warn("logship: "+why+", keeping the lines", "err", err, "requeued", len(back))
	s.down = true
}

// spoolPending appends the pending lines to the spool file, keeping it under SpoolMax.
func (s *Shipper) spoolPending() {
	s.spooled = s.now()
	if len(s.pending) == 0 || s.Spool == "" {
		return
	}
	old, _ := os.ReadFile(s.Spool)
	var b bytes.Buffer
	b.Write(old)
	for _, l := range s.pending {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	out := b.Bytes()
	if over := len(out) - s.SpoolMax; over > 0 { // drop the oldest whole lines
		cut := over
		if i := bytes.IndexByte(out[cut:], '\n'); i >= 0 {
			cut += i + 1
		} else {
			cut = len(out)
		}
		out = out[cut:]
	}
	tmp := s.Spool + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return
	}
	_, werr := f.Write(out)
	serr := f.Sync()
	f.Close()
	if werr != nil || serr != nil || os.Rename(tmp, s.Spool) != nil {
		os.Remove(tmp)
		return
	}
	s.pending, s.size = nil, 0
}
