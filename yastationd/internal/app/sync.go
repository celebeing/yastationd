package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
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
	mu       sync.Mutex
	items    map[string]sharedMsg // kind + "/" + item
	rejected map[string]int64     // kind + "/" + item → the At of a shared value whose trial failed here
	lastAt   atomic.Int64         // the last At this speaker gave (sharedNow)
	bus      publisher            // tests: in place of the MQTT client
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
	"ambilight_fx_brightness"}

// sharedNow: the At of a change, unix ms, never repeating on this speaker (a rejected value is
// known by its At, so two changes in one millisecond must differ).
func (a *App) sharedNow() int64 {
	for {
		last, now := a.shared.lastAt.Load(), time.Now().UnixMilli()
		if now <= last {
			now = last + 1
		}
		if a.shared.lastAt.CompareAndSwap(last, now) {
			return now
		}
	}
}

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
		return settingShared(k, a.cfg()), true
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

// settingShared is a key's value as shared: null while c leaves it at the default (the file does
// not set it, or sets the default), so every speaker keeps its own build's default and a newer
// default reaches them all; its lines otherwise.
func settingShared(k *config.Key, c *config.Config) json.RawMessage {
	v := k.Get(c)
	d := config.Default()
	if !c.Has(k.Name) || slices.Equal(v, k.Get(&d)) {
		return json.RawMessage("null")
	}
	b, _ := json.Marshal(v)
	return b
}

// normShared: a shared setting equal to this build's default counts as the default (null), so a
// value shared before the default changed doesn't pin it here.
func normShared(kind, item string, v json.RawMessage) json.RawMessage {
	k := config.Lookup(item)
	var vals []string
	if kind != "setting" || k == nil || json.Unmarshal(v, &vals) != nil || vals == nil {
		return v
	}
	if d := config.Default(); slices.Equal(vals, k.Get(&d)) {
		return json.RawMessage("null")
	}
	return v
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
	if !on {
		a.sharedSet(kind, item, nil)
		bus.Publish(a.topics.Shared(kind, item), "")
		return true
	}
	v, ok := a.localShared(kind, item)
	if !ok {
		return false
	}
	a.sendShared(bus, kind, item, v)
	return true
}

// sendShared stamps v as this speaker's change of an item, keeps it, and publishes it.
func (a *App) sendShared(bus publisher, kind, item string, v json.RawMessage) {
	m := sharedMsg{V: v, At: a.sharedNow(), By: a.cfg().DeviceID}
	b, _ := json.Marshal(m)
	a.sharedSet(kind, item, &m)
	bus.Publish(a.topics.Shared(kind, item), string(b))
}

// shareChanged: after a local change of an item, a shared one goes out to the others (nothing
// when it already holds the shared value, e.g. it was just taken from there).
func (a *App) shareChanged(kind, item string) {
	m, ok := a.sharedGet(kind, item)
	if !ok {
		return
	}
	if v, ok := a.localShared(kind, item); ok && !sameJSON(v, normShared(kind, item, m.V)) {
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
	if v, ok := a.localShared(kind, item); !ok || sameJSON(v, normShared(kind, item, m.V)) {
		return
	}
	if a.syncRejected(kind+"/"+item) == m.At {
		return // this value failed its trial here; a later change is tried again
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
		var vals []string // nil (null, or this build's default): the key leaves the file
		if err := json.Unmarshal(normShared(kind, item, v), &vals); err != nil {
			return err
		}
		restart, _, err := a.applyConfigKeys(map[string][]string{item: vals}, "shared by the other speakers", "sync")
		if err == nil && restart {
			m, _ := a.sharedGet(kind, item)
			a.notePendingSync(kind+"/"+item, m.At)
		}
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
		c[obj] = func(p []byte) {
			fn(p)
			// every shared option: a handler may change another option too
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
	// Rejected: shared settings whose value failed its trial here and is not taken
	// ("setting/<key>"; until the item changes again).
	Rejected []string `json:"rejected"`
}

type sharedAt struct {
	At int64  `json:"at"`
	By string `json:"by"`
}

func (a *App) syncViewNow() syncView {
	v := syncView{Connected: a.sharedBus() != nil, Settings: map[string]sharedAt{},
		Options: map[string]sharedAt{}, Moments: map[string]sharedAt{}, OptionIDs: []string{}}
	a.shared.mu.Lock()
	v.Rejected = []string{}
	for key, at := range a.shared.rejected {
		if m, ok := a.shared.items[key]; ok && m.At == at {
			v.Rejected = append(v.Rejected, key)
		}
	}
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

// splitShared splits a shared item's key (kind + "/" + item) at its last slash: a moment's kind
// has one of its own.
func splitShared(key string) (kind, item string) {
	i := strings.LastIndexByte(key, '/')
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
		if json.NewDecoder(io.LimitReader(r.Body, smallBody)).Decode(&in) != nil || in.Item == "" {
			writeErr(w, http.StatusBadRequest, "want {kind, item, on}")
			return
		}
		kind := in.Kind
		if kind == "moment" {
			kind = a.momentKind()
		}
		if _, ok := a.localShared(kind, in.Item); !ok {
			writeErr(w, http.StatusUnprocessableEntity, in.Kind+" "+in.Item+": cannot be shared")
			return
		}
		if !a.publishShared(kind, in.Item, in.On) {
			writeErr(w, http.StatusServiceUnavailable, "no MQTT broker: sharing goes through it")
			return
		}
		writeJSON(w, http.StatusOK, a.syncViewNow())
	default:
		writeErr(w, http.StatusMethodNotAllowed, "GET or POST")
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
		v := settingShared(k, c)
		if sameJSON(v, normShared("setting", name, m.V)) {
			continue
		}
		a.sendShared(bus, "setting", name, v)
	}
}

// A shared restart key goes through the trial like any other; when the trial fails, the speaker
// must not take the same value again on the next connect (a restart loop). <cfw>/sync.pending
// lists the shared values a running trial came from; the trial's acceptance removes it. Found at
// start with no trial running, the trial was rolled back (by the daemon or the supervisor): its
// values go to <cfw>/sync.rejected and are skipped until the item changes again.
const (
	syncPendingFile  = "sync.pending"
	syncRejectedFile = "sync.rejected"
)

func (a *App) cfwFile(name string) string {
	return a.cfg().Path(a.prof.Platform.CFWRoot + "/" + name)
}

func readSyncMap(path string) map[string]int64 {
	m := map[string]int64{}
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, &m)
	}
	return m
}

func writeSyncMap(path string, m map[string]int64) {
	b, _ := json.Marshal(m)
	os.WriteFile(path, b, 0o644)
}

func (a *App) notePendingSync(key string, at int64) {
	path := a.cfwFile(syncPendingFile)
	m := readSyncMap(path)
	m[key] = at
	writeSyncMap(path, m)
}

// syncAccepted: the trial passed; its shared values stay.
func (a *App) syncAccepted() { os.Remove(a.cfwFile(syncPendingFile)) }

// loadSyncRejected runs at start, before the broker: a pending list without a running trial means
// the trial was rolled back.
func (a *App) loadSyncRejected() {
	rejected := readSyncMap(a.cfwFile(syncRejectedFile))
	if _, err := os.Stat(a.trialMarker()); err != nil {
		if pending := readSyncMap(a.cfwFile(syncPendingFile)); len(pending) > 0 {
			for k, at := range pending {
				rejected[k] = at
				a.log.Warn("sync: a shared value failed its trial here, not taken again until it changes", "item", k)
			}
			writeSyncMap(a.cfwFile(syncRejectedFile), rejected)
			os.Remove(a.cfwFile(syncPendingFile))
		}
	}
	a.shared.mu.Lock()
	a.shared.rejected = rejected
	a.shared.mu.Unlock()
}

func (a *App) syncRejected(key string) int64 {
	a.shared.mu.Lock()
	defer a.shared.mu.Unlock()
	return a.shared.rejected[key]
}
