package app

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// The diagnostics archive (docs/diagnostics.md): GET /api/diag[?raw=1] runs <cfw>/bin/diag.sh and
// hands its tar.gz to the browser, redacted unless raw. One run at a time; the script bounds each
// of its commands, diagTimeout bounds the whole.

const diagTimeout = 2 * time.Minute

func (a *App) apiDiag(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET"})
		return
	}
	script := a.cfg().Path(a.prof.Platform.CFWRoot + "/bin/diag.sh")
	if _, err := os.Stat(script); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "diag.sh is not installed on this speaker (an older deploy)"})
		return
	}
	if !a.diagBusy.CompareAndSwap(false, true) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "a diagnostics run is already going"})
		return
	}
	defer a.diagBusy.Store(false)
	args := []string{script}
	if r.URL.Query().Get("raw") == "1" {
		args = append(args, "-r")
	}
	args = append(args, "-")
	ctx, cancel := context.WithTimeout(r.Context(), diagTimeout)
	defer cancel()
	var out, errb bytes.Buffer
	cmd := exec.CommandContext(ctx, "sh", args...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	start := time.Now()
	err := cmd.Run()
	b := out.Bytes()
	if err != nil || len(b) < 2 || b[0] != 0x1f || b[1] != 0x8b { // not a gzip
		tail := strings.TrimSpace(errb.String())
		if len(tail) > 500 {
			tail = tail[len(tail)-500:]
		}
		a.log.Warn("diag: failed", "err", err, "stderr", tail)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("diag.sh failed: %v %s", err, tail)})
		return
	}
	a.log.Info("diag: archive made", "bytes", len(b), "raw", len(args) == 3, "took", time.Since(start).Round(time.Millisecond))
	name := fmt.Sprintf("diag-%s-%s.tar.gz", a.cfg().DeviceID, time.Now().Format("20060102-150405"))
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Write(b)
}
