package wifisetup

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/config"
)

// Home settings on the setup page: the yastationd.conf keys that depend on the network the speaker
// lives in (docs/wifi-setup.md). Passwords are write-only, like Wi-Fi ones. An edit is checked
// twice before it is written: per field here, then the whole file through the daemon's own parser,
// because a config the daemon can't load would take the setup mode down with it. The daemon picks
// the new values up when setup mode ends (it restarts itself).

type settingsView struct {
	DeviceID       string   `json:"device_id"`
	DeviceName     string   `json:"device_name"`
	MQTTHost       string   `json:"mqtt_host"`
	MQTTPort       int      `json:"mqtt_port"`
	MQTTUser       string   `json:"mqtt_user"`
	MQTTPassSet    bool     `json:"mqtt_pass_set"`
	Probes         []string `json:"probes"`
	NTPServers     []string `json:"ntp_servers"`
	TZ             string   `json:"tz"`
	Sendspin       bool     `json:"sendspin"`
	SendspinServer string   `json:"sendspin_server"`
	SendspinName   string   `json:"sendspin_name"`
	SetupPSKSet    bool     `json:"setup_psk_set"`
	File           string   `json:"file"`
	Pending        bool     `json:"pending"` // saved, applied when setup mode ends
}

// settingsEdit: a nil field is left as it is; "" clears an optional value.
type settingsEdit struct {
	DeviceName     *string   `json:"device_name"`
	MQTTHost       *string   `json:"mqtt_host"`
	MQTTPort       *int      `json:"mqtt_port"`
	MQTTUser       *string   `json:"mqtt_user"`
	MQTTPass       *string   `json:"mqtt_pass"`
	Probes         *[]string `json:"probes"`
	NTPServers     *[]string `json:"ntp_servers"`
	TZ             *string   `json:"tz"`
	Sendspin       *bool     `json:"sendspin"`
	SendspinServer *string   `json:"sendspin_server"`
	SendspinName   *string   `json:"sendspin_name"`
	SetupPSK       *string   `json:"setup_psk"`
}

var (
	reHost  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,253}$`)
	reProbe = regexp.MustCompile(`^([A-Za-z0-9_-]+=)?[A-Za-z0-9._-]+:[0-9]{1,5}(@(broker|pipeline|lan))?$`)
	reWS    = regexp.MustCompile(`^wss?://[A-Za-z0-9._-]+(:[0-9]{1,5})?(/[^\s]*)?$`)
)

func (m *Mode) apiSettings(w http.ResponseWriter, r *http.Request) {
	if m.o.ConfFile == "" {
		httpErr(w, http.StatusNotFound, errors.New("no config file"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		m.getSettings(w)
	case http.MethodPost:
		m.postSettings(w, r)
	default:
		httpErr(w, http.StatusMethodNotAllowed, errors.New("GET or POST"))
	}
}

func (m *Mode) readSettings() ([]byte, config.Config, error) {
	data, err := os.ReadFile(m.o.ConfFile)
	if err != nil && !os.IsNotExist(err) {
		return nil, config.Config{}, err
	}
	c, err := config.Parse(bytes.NewReader(data))
	return data, c, err
}

func (m *Mode) getSettings(w http.ResponseWriter) {
	confMu.Lock()
	_, c, err := m.readSettings()
	pending := m.settingsChanged
	confMu.Unlock()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err)
		return
	}
	v := settingsView{DeviceID: c.DeviceID, DeviceName: c.DeviceName, MQTTHost: c.MQTTHost,
		MQTTPort: c.MQTTPort, MQTTUser: c.MQTTUser, MQTTPassSet: c.MQTTPass != "",
		Probes: c.Probes, NTPServers: c.NTPServers, TZ: c.TZ, Sendspin: c.Sendspin,
		SendspinServer: c.SendspinServer, SendspinName: c.SendspinName,
		SetupPSKSet: c.WifiSetupPSK != "", File: m.o.ConfFile, Pending: pending}
	if v.Probes == nil {
		v.Probes = []string{}
	}
	if v.NTPServers == nil {
		v.NTPServers = []string{}
	}
	writeJSON(w, v)
}

func (m *Mode) postSettings(w http.ResponseWriter, r *http.Request) {
	var e settingsEdit
	if err := readJSON(r, &e); err != nil {
		httpErr(w, http.StatusBadRequest, err)
		return
	}
	changes, err := e.changes()
	if err != nil {
		httpErr(w, http.StatusBadRequest, err)
		return
	}
	if len(changes) == 0 {
		writeJSON(w, map[string]bool{"ok": true})
		return
	}
	confMu.Lock()
	defer confMu.Unlock()
	old, _, err := m.readSettings()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, err)
		return
	}
	data := config.Edit(old, changes)
	if _, err := config.Parse(bytes.NewReader(data)); err != nil {
		httpErr(w, http.StatusBadRequest, fmt.Errorf("the daemon would reject it: %w", err))
		return
	}
	if bytes.Equal(data, old) {
		writeJSON(w, map[string]bool{"ok": true})
		return
	}
	if err := m.backupOnce(m.o.ConfFile, ".settings-backed-up"); err != nil {
		httpErr(w, http.StatusInternalServerError, err)
		return
	}
	if err := WriteConfAtomic(m.o.ConfFile, data); err != nil {
		httpErr(w, http.StatusInternalServerError, err)
		return
	}
	m.settingsChanged = true
	keys := make([]string, 0, len(changes))
	for k := range changes {
		keys = append(keys, k)
	}
	m.o.Log.Info("wifi setup: settings saved", "keys", strings.Join(keys, ","))
	writeJSON(w, map[string]bool{"ok": true})
}

// changes validates the edit and turns it into config.Edit's input.
func (e settingsEdit) changes() (map[string][]string, error) {
	ch := map[string][]string{}
	one := func(k, v string) {
		if v == "" {
			ch[k] = nil
		} else {
			ch[k] = []string{v}
		}
	}
	clean := func(name string, p *string) (string, error) {
		v := strings.TrimSpace(*p)
		if strings.ContainsAny(v, "\r\n") {
			return "", fmt.Errorf("%s: no line breaks", name)
		}
		return v, nil
	}
	if e.DeviceName != nil {
		v, err := clean("name", e.DeviceName)
		if err != nil || v == "" || len(v) > 64 {
			return nil, errors.New("name: 1 to 64 characters")
		}
		one("device_name", v)
	}
	if e.MQTTHost != nil {
		v, _ := clean("broker", e.MQTTHost)
		if v != "" && !reHost.MatchString(v) {
			return nil, errors.New("broker: a host name or IP address")
		}
		one("mqtt_host", v)
	}
	if e.MQTTPort != nil {
		if *e.MQTTPort < 1 || *e.MQTTPort > 65535 {
			return nil, errors.New("broker port: 1 to 65535")
		}
		one("mqtt_port", strconv.Itoa(*e.MQTTPort))
	}
	if e.MQTTUser != nil {
		v, err := clean("broker user", e.MQTTUser)
		if err != nil || strings.ContainsAny(v, " \t") {
			return nil, errors.New("broker user: no spaces")
		}
		one("mqtt_user", v)
	}
	if e.MQTTPass != nil {
		if strings.ContainsAny(*e.MQTTPass, "\r\n") || strings.TrimSpace(*e.MQTTPass) != *e.MQTTPass {
			return nil, errors.New("broker password: no line breaks or spaces at the ends")
		}
		one("mqtt_pass", *e.MQTTPass)
	}
	if e.Probes != nil {
		var vals []string
		for _, p := range *e.Probes {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if !reProbe.MatchString(p) {
				return nil, fmt.Errorf("probe %q: want name=host:port@role (role broker, pipeline or lan)", p)
			}
			vals = append(vals, p)
		}
		ch["probe"] = vals
	}
	if e.NTPServers != nil {
		var vals []string
		for _, s := range *e.NTPServers {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			if !reHost.MatchString(s) {
				return nil, fmt.Errorf("time server %q: a host name or IP address", s)
			}
			vals = append(vals, s)
		}
		ch["ntp_server"] = vals
	}
	if e.TZ != nil {
		v, _ := clean("time zone", e.TZ)
		if v != "" {
			if _, err := time.LoadLocation(v); err != nil {
				return nil, fmt.Errorf("time zone %q: unknown (want e.g. Europe/Moscow)", v)
			}
		}
		one("tz", v)
	}
	if e.Sendspin != nil {
		ch["sendspin"] = []string{strconv.FormatBool(*e.Sendspin)}
	}
	if e.SendspinServer != nil {
		v, _ := clean("Music Assistant", e.SendspinServer)
		if v != "" && !reWS.MatchString(v) {
			return nil, errors.New("Music Assistant: ws://host:port/sendspin, or empty")
		}
		one("sendspin_server", v)
	}
	if e.SendspinName != nil {
		v, err := clean("player name", e.SendspinName)
		if err != nil || len(v) > 64 {
			return nil, errors.New("player name: up to 64 characters")
		}
		one("sendspin_name", v)
	}
	if e.SetupPSK != nil {
		v := *e.SetupPSK
		if v != "" {
			if _, err := encodePSK(v); err != nil || len(v) > 63 {
				return nil, errors.New("setup network password: 8 to 63 printable characters, or empty for an open network")
			}
		}
		one("wifi_setup_psk", v)
	}
	return ch, nil
}
