package app

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// vitals: how the speaker and the daemon are doing, for the web app's card (GET /api/vitals) and
// /metrics. Fields the speaker can't tell are left out.
type vitals struct {
	DaemonUptime int64      `json:"daemon_uptime_s"`
	SystemUptime int64      `json:"system_uptime_s,omitempty"` // since the speaker booted
	Load         []float64  `json:"load,omitempty"`            // 1, 5, 15 min
	CPUs         int        `json:"cpus"`
	MemTotal     int64      `json:"mem_total,omitempty"` // bytes
	MemFree      int64      `json:"mem_free,omitempty"`
	MemAvailable int64      `json:"mem_available,omitempty"`
	DaemonRSS    int64      `json:"daemon_rss,omitempty"`
	FlashTotal   int64      `json:"flash_total,omitempty"` // the file system the CFW lives on
	FlashFree    int64      `json:"flash_free,omitempty"`
	Temps        []zoneTemp `json:"temps,omitempty"`
}

type zoneTemp struct {
	Zone string  `json:"zone"`
	C    float64 `json:"c"`
}

func (a *App) readVitals() vitals {
	v := vitals{DaemonUptime: int64(time.Since(a.start).Seconds()), CPUs: runtime.NumCPU()}
	c := a.cfg()
	if b, err := os.ReadFile(c.Path("/proc/uptime")); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			if s, err := strconv.ParseFloat(f[0], 64); err == nil {
				v.SystemUptime = int64(s)
			}
		}
	}
	if b, err := os.ReadFile(c.Path("/proc/loadavg")); err == nil {
		if f := strings.Fields(string(b)); len(f) >= 3 {
			for _, x := range f[:3] {
				if l, err := strconv.ParseFloat(x, 64); err == nil {
					v.Load = append(v.Load, l)
				}
			}
			if len(v.Load) != 3 {
				v.Load = nil
			}
		}
	}
	v.MemTotal, v.MemFree, v.MemAvailable = memField(c.Path("/proc/meminfo"), "MemTotal:", "MemFree:", "MemAvailable:")
	v.DaemonRSS, _, _ = memField(c.Path("/proc/self/status"), "VmRSS:", "", "")
	var st syscall.Statfs_t
	if syscall.Statfs(c.Path(a.prof.Platform.CFWRoot), &st) == nil {
		v.FlashTotal = int64(st.Blocks) * int64(st.Bsize)
		v.FlashFree = int64(st.Bavail) * int64(st.Bsize)
	}
	zones, _ := filepath.Glob(c.Path("/sys/class/thermal/thermal_zone*"))
	for _, z := range zones {
		typ, err1 := os.ReadFile(filepath.Join(z, "type"))
		t, err2 := os.ReadFile(filepath.Join(z, "temp"))
		mc, err3 := strconv.Atoi(strings.TrimSpace(string(t)))
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		v.Temps = append(v.Temps, zoneTemp{Zone: strings.TrimSpace(string(typ)), C: float64(mc) / 1000})
	}
	return v
}

// memField reads up to three "<key> <n> kB" lines of a /proc file, in bytes.
func memField(path string, keys ...string) (x, y, z int64) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	out := []*int64{&x, &y, &z}
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		for i, k := range keys {
			if k != "" && f[0] == k {
				if kb, err := strconv.ParseInt(f[1], 10, 64); err == nil {
					*out[i] = kb * 1024
				}
			}
		}
	}
	return
}

func (a *App) apiVitals(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.readVitals())
}
