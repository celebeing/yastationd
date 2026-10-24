package wifisetup

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The setup page and its JSON API. Saved passwords are never sent back to the browser: the page
// shows only whether a network has one, and a new password replaces the old.

//go:embed portal.html
var portalHTML []byte

var confMu sync.Mutex // read-modify-write of wpa_supplicant.conf

type netView struct {
	ID       int    `json:"id"`
	SSID     string `json:"ssid"`
	Security string `json:"security"`
	Priority int    `json:"priority"`
	Disabled bool   `json:"disabled"`
	Hidden   bool   `json:"hidden"`
}

type stateView struct {
	Device   string       `json:"device"`
	Conf     string       `json:"conf"`
	Networks []netView    `json:"networks"`
	Scan     []ScanResult `json:"scan"`
}

type netEdit struct {
	SSID     string  `json:"ssid"`
	PSK      *string `json:"psk"`
	Hidden   bool    `json:"hidden"`
	Priority *int    `json:"priority"`
	Disabled *bool   `json:"disabled"`
}

func (m *Mode) servePortal(ctx context.Context, ip net.IP) error {
	host := ip.String()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/state", m.apiState)
	mux.HandleFunc("/api/networks", m.apiNetworks)
	mux.HandleFunc("/api/networks/", m.apiNetwork)
	mux.HandleFunc("/api/finish", m.apiFinish)
	mux.HandleFunc("/api/reboot", m.apiReboot)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.Redirect(w, r, "http://"+host+"/", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(portalHTML)
	})
	// Any other host name is a connectivity check or a page the phone wanted: send it to us, so
	// the phone reports a captive portal and opens the page.
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hn, _, err := net.SplitHostPort(r.Host); err == nil {
			r.Host = hn
		}
		if r.Host != host {
			http.Redirect(w, r, "http://"+host+"/", http.StatusFound)
			return
		}
		mux.ServeHTTP(w, r)
	})
	ln, err := net.Listen("tcp4", net.JoinHostPort(host, "80"))
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (m *Mode) loadConf() (*Conf, error) {
	data, err := os.ReadFile(m.conf)
	if err != nil {
		return nil, err
	}
	return ParseConf(data)
}

// saveConf writes c; the first write of a setup session keeps the file as it was before the
// session next to it (<conf>.before-setup).
func (m *Mode) saveConf(c *Conf) error {
	backup := m.conf + ".before-setup"
	marker := m.markerPath() + ".backed-up"
	if _, err := os.Stat(marker); err != nil {
		if old, err := os.ReadFile(m.conf); err == nil {
			if err := WriteConfAtomic(backup, old); err != nil {
				return err
			}
		}
		os.WriteFile(marker, nil, 0o600)
	}
	return WriteConfAtomic(m.conf, c.Format())
}

func (m *Mode) apiState(w http.ResponseWriter, r *http.Request) {
	confMu.Lock()
	c, err := m.loadConf()
	confMu.Unlock()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err)
		return
	}
	st := stateView{Device: m.o.DeviceName, Conf: m.conf, Networks: []netView{}, Scan: m.scanned}
	if st.Scan == nil {
		st.Scan = []ScanResult{}
	}
	for i, n := range c.Networks {
		st.Networks = append(st.Networks, netView{ID: i, SSID: n.SSID(), Security: n.Security(),
			Priority: n.Priority(), Disabled: n.Disabled(), Hidden: n.Hidden()})
	}
	writeJSON(w, st)
}

// POST /api/networks: add a network; one with the same SSID gets the new settings instead.
func (m *Mode) apiNetworks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, http.StatusMethodNotAllowed, errors.New("POST only"))
		return
	}
	var e netEdit
	if err := readJSON(r, &e); err != nil {
		httpErr(w, http.StatusBadRequest, err)
		return
	}
	psk := ""
	if e.PSK != nil {
		psk = *e.PSK
	}
	prio := 0
	if e.Priority != nil {
		prio = *e.Priority
	}
	confMu.Lock()
	defer confMu.Unlock()
	c, err := m.loadConf()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err)
		return
	}
	n, err := NewNetwork(e.SSID, psk, e.Hidden, prio)
	if err != nil {
		httpErr(w, http.StatusBadRequest, err)
		return
	}
	if i := c.Index(e.SSID); i >= 0 {
		c.Networks[i] = n
	} else {
		c.Networks = append(c.Networks, n)
	}
	if err := m.saveConf(c); err != nil {
		httpErr(w, http.StatusInternalServerError, err)
		return
	}
	m.o.Log.Info("wifi setup: network saved", "open", psk == "", "hidden", e.Hidden)
	writeJSON(w, map[string]bool{"ok": true})
}

// POST /api/networks/<id> edits, DELETE removes; the body's (or query's) ssid must match, so a
// stale page can't hit the wrong entry.
func (m *Mode) apiNetwork(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/api/networks/"))
	if err != nil {
		httpErr(w, http.StatusNotFound, errors.New("no such network"))
		return
	}
	var e netEdit
	switch r.Method {
	case http.MethodDelete:
		e.SSID = r.URL.Query().Get("ssid")
	case http.MethodPost:
		if err := readJSON(r, &e); err != nil {
			httpErr(w, http.StatusBadRequest, err)
			return
		}
	default:
		httpErr(w, http.StatusMethodNotAllowed, errors.New("POST or DELETE"))
		return
	}
	confMu.Lock()
	defer confMu.Unlock()
	c, err := m.loadConf()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err)
		return
	}
	if id < 0 || id >= len(c.Networks) || c.Networks[id].SSID() != e.SSID {
		httpErr(w, http.StatusConflict, errors.New("the list changed; reload the page"))
		return
	}
	if r.Method == http.MethodDelete {
		c.Networks = append(c.Networks[:id], c.Networks[id+1:]...)
	} else {
		n := c.Networks[id]
		if e.PSK != nil {
			if err := n.SetPSK(*e.PSK); err != nil {
				httpErr(w, http.StatusBadRequest, err)
				return
			}
		}
		if e.Priority != nil {
			if *e.Priority == 0 {
				n.Del("priority")
			} else {
				n.Set("priority", strconv.Itoa(*e.Priority))
			}
		}
		if e.Disabled != nil {
			if *e.Disabled {
				n.Set("disabled", "1")
			} else {
				n.Del("disabled")
			}
		}
	}
	if err := m.saveConf(c); err != nil {
		httpErr(w, http.StatusInternalServerError, err)
		return
	}
	m.o.Log.Info("wifi setup: network changed", "id", id, "method", r.Method)
	writeJSON(w, map[string]bool{"ok": true})
}

func (m *Mode) apiFinish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpErr(w, http.StatusMethodNotAllowed, errors.New("POST only"))
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
	go func() {
		time.Sleep(time.Second) // let the reply reach the phone first
		m.Leave()
	}()
}

func (m *Mode) apiReboot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || m.o.Reboot == nil {
		httpErr(w, http.StatusMethodNotAllowed, errors.New("POST only"))
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
	go func() {
		time.Sleep(time.Second)
		m.o.Reboot()
	}()
}

func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, 4096)).Decode(v)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

func httpErr(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
