package app

import (
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// The web app's log view (docs/web-ui.md): the tail of the daemon's log or of the system log (the
// kernel, the Wi-Fi driver, dropbear…), as the speaker has them now. Read-only, behind the login.

const (
	logLinesDefault = 300
	logLinesMax     = 2000
	logTailBytes    = 512 << 10 // read at most this much from a file's end
)

// logSources: what the view can show, by name.
func (a *App) logSources() map[string][]string {
	return map[string][]string{
		"daemon": {a.cfg.Path("/tmp/yastationd.log")},
		// busybox syslogd rotates messages into messages.0; the older one first.
		"system": {a.cfg.Path("/var/log/messages.0"), a.cfg.Path("/var/log/messages")},
	}
}

// apiLog: GET /api/log?source=daemon|system&lines=N -> {"source", "lines": [...]}, oldest first.
func (a *App) apiLog(w http.ResponseWriter, r *http.Request) {
	source := r.URL.Query().Get("source")
	if source == "" {
		source = "daemon"
	}
	files, ok := a.logSources()[source]
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "source: daemon or system"})
		return
	}
	n := logLinesDefault
	if v, err := strconv.Atoi(r.URL.Query().Get("lines")); err == nil && v > 0 {
		n = min(v, logLinesMax)
	}
	var lines []string
	for _, f := range files {
		lines = append(lines, tailLines(f, logTailBytes)...)
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	if lines == nil {
		lines = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"source": source, "lines": lines})
}

// tailLines reads up to max bytes from the end of path and returns its whole lines.
func tailLines(path string, max int64) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil
	}
	off := st.Size() - max
	if off < 0 {
		off = 0
	}
	b, err := io.ReadAll(io.NewSectionReader(f, off, st.Size()-off))
	if err != nil {
		return nil
	}
	s := string(b)
	if off > 0 { // the first line is cut
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
	}
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
