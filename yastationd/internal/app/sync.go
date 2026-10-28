package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/config"
)

// Sync between speakers (docs/settings.md, "Sync"): an item the owner marks shared lives in a
// retained topic, <prefix>/shared/<kind>/<item>, and every speaker takes its value. Kinds:
// "setting" (a config key with Sync in the registry), "option" (an HA config entity: clock
// brightness, durations…), "moment/<display kind>" (a moment, among speakers whose front display
// is of that kind). The broker's order decides between two speakers changing one item at once:
// each takes every message in that order, so all end on the last one. A speaker that was off
// gets the values when it connects. Sharing needs no leader.

// sharedMsg is a shared item's retained payload.
type sharedMsg struct {
	V  json.RawMessage `json:"v"`  // the value: a setting's lines, an option's state, a moment's candidates (null: the defaults)
	At int64           `json:"at"` // unix ms of the change
	By string          `json:"by"` // the speaker it came from
}

type shared struct {
	mu    sync.Mutex
	items map[string]sharedMsg // kind + "/" + item
	bus   publisher            // tests: in place of the MQTT client
}

type publisher interface {
	Publish(topic, payload string) // retained
}

// sharedBus is where shared items go: the broker while connected (nil without one).
func (a *App) sharedBus() publisher {
	if a.shared.bus != nil {
		return a.shared.bus
	}
	if a.mqtt == nil || !a.mqtt.Connected() {
		return nil
	}
	return a.mqtt
}

// syncOptions are the HA config entities that can be shared (those this board has).
var syncOptions = []string{"auto_brightness", "clock_brightness", "text_duration", "animation_duration", "follow_up",
	"display_faces", "ambilight_fx_brightness"}

func (a *App) momentKind() string { return "moment/" + a.frontKind() }

func (a *App) sharedGet(kind, item string) (sharedMsg, bool) {
	a.shared.mu.Lock()
	defer a.shared.mu.Unlock()
	m, ok := a.shared.items[kind+"/"+item]
	return m, ok
}

func (a *App) sharedSet(kind, item string, m *sharedMsg) {
	a.shared.mu.Lock()
	defer a.shared.mu.Unlock()
	if a.shared.items == nil {
		a.shared.items = map[string]sharedMsg{}
	}
	if m == nil {
		delete(a.shared.items, kind+"/"+item)
	} else {
		a.shared.items[kind+"/"+item] = *m
	}
}

// localShared is this speaker's own value of an item, as it would be shared.
func (a *App) localShared(kind, item string) (json.RawMessage, bool) {
	switch {
	case kind == "setting":
		k := config.Lookup(item)
		if k == nil || !k.Sync {
			return nil, false
		}
		b, _ := json.Marshal(k.Get(a.cfg()))
		return b, true
	case kind == "option":
		v, ok := a.states.snapshot()[item]
		if !ok {
			return nil, false
		}
		b, _ := json.Marshal(v.value)
		return b, true
	case kind == a.momentKind() && a.frontKind() != "":
		shows, ok := a.userMomentMap()[item]
		if !ok {
			return json.RawMessage("null"), true
		}
		b, _ := json.Marshal(shows)
		return b, true
	}
	return nil, false
}

func sameJSON(a, b json.RawMessage) bool {
	var x, y bytes.Buffer
	if json.Compact(&x, a) != nil || json.Compact(&y, b) != nil {
		return false
	}
	return bytes.Equal(x.Bytes(), y.Bytes())
}

// publishShared shares this speaker's value of an item (on = false: stops sharing it).
func (a *App) publishShared(kind, item string, on bool) bool {
	bus := a.sharedBus()
	if bus == nil {
		return false
	}
	topic := a.topics.Shared(kind, item)
	if !on {
		a.sharedSet(kind, item, nil)
		bus.Publish(topic, "")
		return true
	}
	v, ok := a.localShared(kind, item)
	if !ok {
		return false
	}
	m := sharedMsg{V: v, At: time.Now().UnixMilli(), By: a.cfg().DeviceID}
	b, _ := json.Marshal(m)
	a.sharedSet(kind, item, &m)
	bus.Publish(topic, string(b))
	return true
}

// shareChanged: after a local change of an item, a shared one goes out to the others (nothing
// when it already holds the shared value, e.g. it was just taken from there).
func (a *App) shareChanged(kind, item string) {
	m, ok := a.sharedGet(kind, item)
	if !ok {
		return
	}
	if v, ok := a.localShared(kind, item); ok && !sameJSON(v, m.V) {
		a.publishShared(kind, item, true)
	}
}

// syncKinds: the kinds of shared items this speaker takes.
func (a *App) syncKinds() []string {
	kinds := []string{"setting", "option"}
	if a.frontKind() != "" {
		kinds = append(kinds, a.momentKind())
	}
	return kinds
}

func (a *App) syncSubscribe() {
	for _, kind := range a.syncKinds() {
		kind := kind
		a.mqtt.SubscribeTopic(a.topics.SharedAll(kind), func(topic string, payload []byte) {
			if item := a.topics.SharedItem(kind, topic); item != "" {
				a.onShared(kind, item, payload)
			}
		})
	}
}

// onShared takes a shared item's value (an empty payload: no longer shared; the value stays).
func (a *App) onShared(kind, item string, payload []byte) {
	if len(payload) == 0 {
		a.sharedSet(kind, item, nil)
		return
	}
	var m sharedMsg
	if json.Unmarshal(payload, &m) != nil || len(m.V) == 0 {
		return
	}
	a.sharedSet(kind, item, &m)
	if v, ok := a.localShared(kind, item); !ok || sameJSON(v, m.V) {
		return
	}
	if err := a.applyShared(kind, item, m.V); err != nil {
		a.log.Warn("sync: not taken", "kind", kind, "item", item, "from", m.By, "err", err)
		return
	}
	a.log.Info("sync: taken", "kind", kind, "item", item, "from", m.By)
}

func (a *App) applyShared(kind, item string, v json.RawMessage) error {
	switch {
	case kind == "setting":
		var vals []string
		if err := json.Unmarshal(v, &vals); err != nil {
			return err
		}
		old, err := os.ReadFile(a.cfg().File)
		if err != nil {
			return err
		}
		_, _, err = a.applyConfigText(config.EditNote(old, map[string][]string{item: vals}, "shared by the other speakers"), "sync")
		return err
	case kind == "option":
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return err
		}
		if fn := a.commands()[item]; fn != nil {
			fn([]byte(s))
		}
		return nil
	default: // a moment
		var shows []show
		if err := json.Unmarshal(v, &shows); err != nil {
			return err
		}
		if string(v) == "null" {
			shows = nil
		}
		return a.setMoment(item, shows)
	}
}

// shareOptions wraps the shareable options' handlers so a local change goes out when shared.
func (a *App) shareOptions(c map[string]func([]byte)) {
	for _, obj := range syncOptions {
		fn := c[obj]
		if fn == nil {
			continue
		}
		obj := obj
		c[obj] = func(p []byte) {
			fn(p)
			// every shared option: one can change another (a manual clock brightness turns
			// auto brightness off)
			for _, o := range syncOptions {
				a.shareChanged("option", o)
			}
		}
	}
}

// syncView: what this speaker shares, for the settings page.
type syncView struct {
	Connected bool                `json:"connected"`
	Settings  map[string]sharedAt `json:"settings"`
	Options   map[string]sharedAt `json:"options"`
	Moments   map[string]sharedAt `json:"moments"`
	OptionIDs []string            `json:"option_ids"` // the options this speaker can share
}

type sharedAt struct {
	At int64  `json:"at"`
	By string `json:"by"`
}

func (a *App) syncViewNow() syncView {
	v := syncView{Connected: a.sharedBus() != nil, Settings: map[string]sharedAt{},
		Options: map[string]sharedAt{}, Moments: map[string]sharedAt{}, OptionIDs: []string{}}
	a.shared.mu.Lock()
	for key, m := range a.shared.items {
		at := sharedAt{m.At, m.By}
		switch kind, item := splitShared(key); {
		case kind == "setting":
			v.Settings[item] = at
		case kind == "option":
			v.Options[item] = at
		case kind == a.momentKind():
			v.Moments[item] = at
		}
	}
	a.shared.mu.Unlock()
	cmds := a.commands()
	for _, o := range syncOptions {
		if cmds[o] != nil {
			v.OptionIDs = append(v.OptionIDs, o)
		}
	}
	sort.Strings(v.OptionIDs)
	return v
}

func splitShared(key string) (kind, item string) {
	i := len(key) - 1
	for i >= 0 && key[i] != '/' {
		i--
	}
	return key[:max(i, 0)], key[i+1:]
}

// apiSync: GET what is shared; POST {"kind": "setting"|"option"|"moment", "item": …, "on": bool}
// starts sharing this speaker's value of the item, or stops sharing it.
func (a *App) apiSync(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, a.syncViewNow())
	case http.MethodPost:
		var in struct {
			Kind string `json:"kind"`
			Item string `json:"item"`
			On   bool   `json:"on"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&in) != nil || in.Item == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "want {kind, item, on}"})
			return
		}
		kind := in.Kind
		if kind == "moment" {
			kind = a.momentKind()
		}
		if _, ok := a.localShared(kind, in.Item); !ok {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": in.Kind + " " + in.Item + ": cannot be shared"})
			return
		}
		if !a.publishShared(kind, in.Item, in.On) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no MQTT broker: sharing goes through it"})
			return
		}
		writeJSON(w, http.StatusOK, a.syncViewNow())
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET or POST"})
	}
}

// shareSettings: changed keys that are shared go out with their values in c (the config being
// applied; on a restart it is not in force yet).
func (a *App) shareSettings(c *config.Config, changed []string) {
	bus := a.sharedBus()
	if bus == nil {
		return
	}
	for _, name := range changed {
		m, ok := a.sharedGet("setting", name)
		k := config.Lookup(name)
		if !ok || k == nil || !k.Sync {
			continue
		}
		v, _ := json.Marshal(k.Get(c))
		if sameJSON(v, m.V) {
			continue
		}
		next := sharedMsg{V: v, At: time.Now().UnixMilli(), By: a.cfg().DeviceID}
		b, _ := json.Marshal(next)
		a.sharedSet("setting", name, &next)
		bus.Publish(a.topics.Shared("setting", name), string(b))
	}
}
