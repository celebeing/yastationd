package wifisetup

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The firmware's own wpa_supplicant (started by init's S40wifi) is found by its command line, so
// it can be stopped for the access point and started again with exactly the same arguments.

type wpaProc struct {
	pid   int
	argv  []string // argv[0] is the binary
	iface string   // -i
	conf  string   // -c
}

// findWPA looks for a running wpa_supplicant under procRoot ("/proc").
func findWPA(procRoot string) (wpaProc, bool) {
	ents, _ := os.ReadDir(procRoot)
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(procRoot, e.Name(), "cmdline"))
		if err != nil || len(raw) == 0 {
			continue
		}
		argv := strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
		if filepath.Base(argv[0]) != "wpa_supplicant" {
			continue
		}
		p := wpaProc{pid: pid, argv: argv}
		p.iface, p.conf = optArg(argv, "-i"), optArg(argv, "-c")
		return p, true
	}
	return wpaProc{}, false
}

// optArg returns the value of a short option given as "-cVALUE" or "-c VALUE".
func optArg(argv []string, opt string) string {
	for i, a := range argv[1:] {
		if a == opt && i+2 < len(argv) {
			return argv[i+2]
		}
		if strings.HasPrefix(a, opt) && len(a) > len(opt) {
			return a[len(opt):]
		}
	}
	return ""
}

// ScanResult is one network seen in the scan before the access point came up.
type ScanResult struct {
	SSID     string `json:"ssid"`
	Signal   int    `json:"signal"` // dBm
	Freq     int    `json:"freq"`   // MHz
	Security string `json:"security"`
}

// scan asks the running wpa_supplicant for a fresh scan and returns what it saw, strongest
// first, one entry per SSID (hidden networks dropped).
func scan(iface string, wait time.Duration) []ScanResult {
	exec.Command("wpa_cli", "-i", iface, "scan").Run()
	deadline := time.Now().Add(wait)
	var res []ScanResult
	for {
		time.Sleep(time.Second)
		out, err := exec.Command("wpa_cli", "-i", iface, "scan_results").Output()
		if err == nil {
			res = parseScanResults(out)
		}
		if time.Now().After(deadline) {
			return res
		}
	}
}

func parseScanResults(out []byte) []ScanResult {
	best := map[string]ScanResult{}
	for _, line := range bytes.Split(out, []byte("\n")) {
		f := strings.SplitN(string(line), "\t", 5)
		if len(f) != 5 {
			continue
		}
		freq, err1 := strconv.Atoi(f[1])
		sig, err2 := strconv.Atoi(f[2])
		ssid := unescape(f[4])
		if err1 != nil || err2 != nil || ssid == "" || strings.Trim(ssid, "\x00") == "" {
			continue
		}
		r := ScanResult{SSID: ssid, Signal: sig, Freq: freq, Security: flagsSecurity(f[3])}
		if old, ok := best[ssid]; !ok || r.Signal > old.Signal {
			best[ssid] = r
		}
	}
	res := make([]ScanResult, 0, len(best))
	for _, r := range best {
		res = append(res, r)
	}
	sort.Slice(res, func(i, j int) bool {
		if res[i].Signal != res[j].Signal {
			return res[i].Signal > res[j].Signal
		}
		return res[i].SSID < res[j].SSID
	})
	return res
}

func flagsSecurity(flags string) string {
	switch {
	case strings.Contains(flags, "EAP"):
		return "eap"
	case strings.Contains(flags, "PSK"), strings.Contains(flags, "SAE"):
		return "psk"
	case strings.Contains(flags, "WEP"):
		return "wep"
	}
	return "open"
}
