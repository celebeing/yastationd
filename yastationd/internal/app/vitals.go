package app

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/version"
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

// serveMetrics: Prometheus text — the numeric and on/off states the app publishes, and a few
// process numbers. No login (read-only, the exporter convention).
func (a *App) serveMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	dev := a.cfg().DeviceID
	fmt.Fprintf(w, "# TYPE yastation_info gauge\nyastation_info{device=%q,board=%q,version=%q} 1\n", dev, a.prof.Name, version.Version)
	fmt.Fprintf(w, "# TYPE yastation_uptime_seconds gauge\nyastation_uptime_seconds{device=%q} %d\n", dev, int(time.Since(a.start).Seconds()))
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	fmt.Fprintf(w, "# TYPE yastation_go_heap_bytes gauge\nyastation_go_heap_bytes{device=%q} %d\n", dev, ms.HeapAlloc)
	fmt.Fprintf(w, "# TYPE yastation_goroutines gauge\nyastation_goroutines{device=%q} %d\n", dev, runtime.NumGoroutine())
	if b, err := os.ReadFile("/proc/self/statm"); err == nil {
		if f := strings.Fields(string(b)); len(f) > 1 {
			if pages, err := strconv.Atoi(f[1]); err == nil {
				fmt.Fprintf(w, "# TYPE yastation_rss_bytes gauge\nyastation_rss_bytes{device=%q} %d\n", dev, pages*os.Getpagesize())
			}
		}
	}
	a.systemMetrics(w, dev)
	st := a.states.snapshot()
	keys := make([]string, 0, len(st))
	for k := range st {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := st[k].value
		var f float64
		switch v {
		case "ON":
			f = 1
		case "OFF":
			f = 0
		default:
			var err error
			if f, err = strconv.ParseFloat(v, 64); err != nil {
				if k == "health" { // a state as a label
					fmt.Fprintf(w, "yastation_health{device=%q,state=%q} 1\n", dev, v)
				}
				continue
			}
		}
		name := "yastation_" + metricName(k)
		fmt.Fprintf(w, "# TYPE %s gauge\n%s{device=%q} %s\n", name, name, dev, strconv.FormatFloat(f, 'g', -1, 64))
	}
}

func metricName(s string) string {
	b := []byte(s)
	for i, c := range b {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			b[i] = '_'
		}
	}
	return string(b)
}

// systemMetrics: the speaker's memory, load, temperatures (every thermal zone, by its type) and
// uptime.
func (a *App) systemMetrics(w io.Writer, dev string) {
	v := a.readVitals()
	if v.MemTotal > 0 {
		fmt.Fprintf(w, "# TYPE yastation_memory_bytes gauge\n")
		fmt.Fprintf(w, "yastation_memory_bytes{device=%q,kind=\"total\"} %d\n", dev, v.MemTotal)
		fmt.Fprintf(w, "yastation_memory_bytes{device=%q,kind=\"free\"} %d\n", dev, v.MemFree)
		fmt.Fprintf(w, "yastation_memory_bytes{device=%q,kind=\"available\"} %d\n", dev, v.MemAvailable)
	}
	if len(v.Load) == 3 {
		fmt.Fprintf(w, "# TYPE yastation_load gauge\n")
		for i, span := range []string{"1m", "5m", "15m"} {
			fmt.Fprintf(w, "yastation_load{device=%q,span=%q} %s\n", dev, span, strconv.FormatFloat(v.Load[i], 'f', 2, 64))
		}
	}
	if len(v.Temps) > 0 {
		fmt.Fprintf(w, "# TYPE yastation_temperature_celsius gauge\n")
		for _, t := range v.Temps {
			fmt.Fprintf(w, "yastation_temperature_celsius{device=%q,zone=%q} %s\n", dev, t.Zone, strconv.FormatFloat(t.C, 'f', 1, 64))
		}
	}
	if v.SystemUptime > 0 {
		fmt.Fprintf(w, "# TYPE yastation_system_uptime_seconds gauge\nyastation_system_uptime_seconds{device=%q} %d\n", dev, v.SystemUptime)
	}
}
