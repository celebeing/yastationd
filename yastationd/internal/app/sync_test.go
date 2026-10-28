package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/celebeing/yastationd/yastationd/internal/mqtt"
)

// fakeBroker: retained topics and in-order delivery to every attached speaker, as a broker does.
// Publishing during a delivery queues the message, so the order is the broker's.
type fakeBroker struct {
	mu       sync.Mutex
	retained map[string]string
	queue    [][2]string
	apps     []*App
}

type fakeClient struct {
	b *fakeBroker
}

func (c fakeClient) Publish(topic, payload string) {
	c.b.mu.Lock()
	c.b.queue = append(c.b.queue, [2]string{topic, payload})
	c.b.mu.Unlock()
}

func newBroker() *fakeBroker { return &fakeBroker{retained: map[string]string{}} }

// attach connects a speaker: it gets the retained items first, as on a real connect.
func (b *fakeBroker) attach(a *App) {
	a.shared.bus = fakeClient{b}
	b.mu.Lock()
	b.apps = append(b.apps, a)
	ret := make(map[string]string, len(b.retained))
	for k, v := range b.retained {
		ret[k] = v
	}
	b.mu.Unlock()
	for topic, payload := range ret {
		deliver(a, topic, payload)
	}
	b.flush()
}

func deliver(a *App, topic, payload string) {
	for _, kind := range a.syncKinds() {
		if item := a.topics.SharedItem(kind, topic); item != "" {
			a.onShared(kind, item, []byte(payload))
		}
	}
}

// flush delivers every queued message, including the ones the deliveries publish.
func (b *fakeBroker) flush() {
	for {
		b.mu.Lock()
		if len(b.queue) == 0 {
			b.mu.Unlock()
			return
		}
		m := b.queue[0]
		b.queue = b.queue[1:]
		if m[1] == "" {
			delete(b.retained, m[0])
		} else {
			b.retained[m[0]] = m[1]
		}
		apps := append([]*App(nil), b.apps...)
		b.mu.Unlock()
		for _, a := range apps {
			deliver(a, m[0], m[1])
		}
	}
}

// speaker: a web test app with its own id and config file.
func speaker(t *testing.T, id string) *App {
	a, _ := newWebApp(t)
	text := "device_id = " + id + "\n"
	os.WriteFile(a.cfg().File, []byte(text), 0o600)
	a.adoptConfig([]byte(text))
	a.prof.Platform.CFWRoot = filepath.Dir(a.cfg().File)
	return a
}

func setKey(t *testing.T, a *App, key, val string) (restart bool) {
	t.Helper()
	old, _ := os.ReadFile(a.cfg().File)
	restart, _, err := a.applyConfigText(configEdit(old, key, val), "test")
	if err != nil {
		t.Fatal(err)
	}
	return restart
}

func configEdit(old []byte, key, val string) []byte {
	return []byte(strings.TrimRight(string(old), "\n") + "\n" + key + " = " + val + "\n")
}

func TestSyncTakesSharedSetting(t *testing.T) {
	a, _ := newWebApp(t)
	a.onShared("setting", "volume_step", []byte(`{"v":["3"],"at":1,"by":"other"}`))
	if a.cfg().VolumeStep != 3 {
		t.Fatalf("not taken: %d", a.cfg().VolumeStep)
	}
	if b, _ := os.ReadFile(a.cfg().File); !strings.Contains(string(b), "volume_step = 3") {
		t.Fatalf("not written: %q", b)
	}
	if _, ok := a.sharedGet("setting", "volume_step"); !ok {
		t.Fatal("not recorded as shared")
	}
	// The same value again changes nothing (no rewrite, so no loop between speakers).
	os.WriteFile(a.cfg().File+".bak", nil, 0o600)
	a.onShared("setting", "volume_step", []byte(`{"v":["3"],"at":2,"by":"other"}`))
	if b, _ := os.ReadFile(a.cfg().File + ".bak"); len(b) != 0 {
		t.Fatal("rewritten for an equal value")
	}
	// Unshared: the value stays.
	a.onShared("setting", "volume_step", nil)
	if _, ok := a.sharedGet("setting", "volume_step"); ok || a.cfg().VolumeStep != 3 {
		t.Fatal("unshare")
	}
	// Not shareable (a speaker's own), out of range, or garbage: ignored.
	a.onShared("setting", "device_id", []byte(`{"v":["x"]}`))
	a.onShared("setting", "volume_max", []byte(`{"v":["300"]}`))
	a.onShared("setting", "volume_max", []byte(`{"v":`))
	a.onShared("setting", "no_such_key", []byte(`{"v":["1"]}`))
	if a.cfg().DeviceID != "t1" || a.cfg().VolumeMax != 100 {
		t.Fatalf("took what it must not: %q %d", a.cfg().DeviceID, a.cfg().VolumeMax)
	}
}

func TestSyncTwoSpeakersConverge(t *testing.T) {
	b := newBroker()
	s1, s2 := speaker(t, "s1"), speaker(t, "s2")
	b.attach(s1)
	b.attach(s2)
	setKey(t, s1, "volume_step", "2")
	b.flush()
	if s2.cfg().VolumeStep != 1 {
		t.Fatal("an unshared key went across")
	}
	// Sharing gives every speaker s1's value.
	if !s1.publishShared("setting", "volume_step", true) {
		t.Fatal("share")
	}
	b.flush()
	if s2.cfg().VolumeStep != 2 {
		t.Fatalf("s2 did not take it: %d", s2.cfg().VolumeStep)
	}
	// A change on either side goes to the other.
	setKey(t, s2, "volume_step", "4")
	b.flush()
	if s1.cfg().VolumeStep != 4 {
		t.Fatalf("s1 did not take s2's change: %d", s1.cfg().VolumeStep)
	}
	// Both change at once: both end on the change the broker got last.
	setKey(t, s1, "volume_step", "1")
	setKey(t, s2, "volume_step", "3")
	b.flush()
	if s1.cfg().VolumeStep != 3 || s2.cfg().VolumeStep != 3 {
		t.Fatalf("no convergence: %d %d", s1.cfg().VolumeStep, s2.cfg().VolumeStep)
	}
	// A speaker that connects later takes the shared value.
	s3 := speaker(t, "s3")
	b.attach(s3)
	if s3.cfg().VolumeStep != 3 {
		t.Fatalf("late speaker: %d", s3.cfg().VolumeStep)
	}
	// Stop sharing: every speaker keeps its value, later changes stay local.
	s2.publishShared("setting", "volume_step", false)
	b.flush()
	if _, ok := s1.sharedGet("setting", "volume_step"); ok {
		t.Fatal("s1 still sees it shared")
	}
	setKey(t, s1, "volume_step", "2")
	b.flush()
	if s2.cfg().VolumeStep != 3 || s3.cfg().VolumeStep != 3 {
		t.Fatal("a change went out after sharing stopped")
	}
	// The speakers' own keys never go out, shared or not.
	if s1.publishShared("setting", "device_name", true) {
		t.Fatal("device_name shared")
	}
}

func TestSyncSettingNeedingRestart(t *testing.T) {
	b := newBroker()
	s1, s2 := speaker(t, "s1"), speaker(t, "s2")
	var quits atomic.Int32
	s2.quit = func() { quits.Add(1) }
	b.attach(s1)
	b.attach(s2)
	s1.publishShared("setting", "wake_enabled", true)
	b.flush()
	if quits.Load() != 0 {
		t.Fatal("restarted for an equal value")
	}
	// s1 changes it (its own restart: the value goes out first, from the config being applied).
	s1.quit = func() {}
	if !setKey(t, s1, "wake_enabled", "false") {
		t.Fatal("wake_enabled needs a restart")
	}
	b.flush()
	if b2, _ := os.ReadFile(s2.cfg().File); !strings.Contains(string(b2), "wake_enabled = false") {
		t.Fatalf("s2's file: %q", b2)
	}
	if _, err := os.Stat(s2.trialMarker()); err != nil {
		t.Fatal("s2 restarts on trial")
	}
}

// A new speaker joins a household that shares several restart keys: it takes them all, and a
// rollback of the trial would bring back its config from before the first of them.
func TestSyncNewSpeakerTakesAll(t *testing.T) {
	b := newBroker()
	s1 := speaker(t, "s1")
	b.attach(s1)
	s1.quit = func() {}
	setKey(t, s1, "volume_step", "3")
	setKey(t, s1, "wake_enabled", "false")
	setKey(t, s1, "room_sound", "false")
	text, _ := os.ReadFile(s1.cfg().File) // as after s1's restart
	s1.adoptConfig(text)
	for _, k := range []string{"volume_step", "wake_enabled", "room_sound"} {
		s1.publishShared("setting", k, true)
	}
	b.flush()

	s2 := speaker(t, "s2")
	before, _ := os.ReadFile(s2.cfg().File)
	var quits atomic.Int32
	s2.quit = func() { quits.Add(1) }
	b.attach(s2)
	got, _ := os.ReadFile(s2.cfg().File)
	for _, line := range []string{"volume_step = 3", "wake_enabled = false", "room_sound = false"} {
		if !strings.Contains(string(got), line) {
			t.Fatalf("new speaker lacks %q: %q", line, got)
		}
	}
	// (runtime keys taken before the first restart key were applied live and may be in it)
	if bak, _ := os.ReadFile(s2.cfg().File + ".bak"); !strings.HasPrefix(string(bak), string(before)) ||
		strings.Contains(string(bak), "wake_enabled") || strings.Contains(string(bak), "room_sound") {
		t.Fatalf(".bak is not the config from before the restart keys: %q", bak)
	}
	// After its restart the same retained values change nothing.
	text, _ = os.ReadFile(s2.cfg().File)
	s2.adoptConfig(text)
	s2.restartPending.Store(false)
	os.Remove(s2.trialMarker())
	os.Remove(s2.cfg().File + ".bak")
	b.attach(s2)
	if _, err := os.Stat(s2.cfg().File + ".bak"); err == nil {
		t.Fatal("rewritten after the restart")
	}
}

func TestSyncOptions(t *testing.T) {
	b := newBroker()
	s1, s2 := speaker(t, "s1"), speaker(t, "s2")
	b.attach(s1)
	b.attach(s2)
	if !s1.publishShared("option", "clock_brightness", true) || !s1.publishShared("option", "auto_brightness", true) {
		t.Fatal("share")
	}
	b.flush()
	// Two shared options go across together.
	s2.commands()["clock_brightness"]([]byte("40"))
	b.flush()
	if st := s1.store.get(); st.ClockBright != 40 || st.ClockAuto {
		t.Fatalf("s1: bright %d auto %v", st.ClockBright, st.ClockAuto)
	}
	s1.commands()["auto_brightness"]([]byte("ON"))
	b.flush()
	if !s2.store.get().ClockAuto {
		t.Fatal("auto brightness did not go across")
	}
	// Unshared options stay local.
	s1.commands()["text_duration"]([]byte("33"))
	b.flush()
	if s2.store.get().TextSecs == 33 {
		t.Fatal("an unshared option went across")
	}
}

func momentSpeaker(t *testing.T, id string) *App {
	a := newExpressApp(t, true)
	a.topics = mqtt.NewTopics("yastation", "homeassistant", id)
	c := a.cfg()
	c.DeviceID = id
	a.prof.Platform.CFWRoot = t.TempDir()
	return a
}

func TestSyncMoments(t *testing.T) {
	b := newBroker()
	s1, s2 := momentSpeaker(t, "s1"), momentSpeaker(t, "s2")
	pix := speaker(t, "p1") // no front display: takes no moments
	b.attach(s1)
	b.attach(s2)
	b.attach(pix)
	if !s1.publishShared(s1.momentKind(), "listening", true) {
		t.Fatal("share")
	}
	b.flush()
	if err := s1.setMoment("listening", []show{{Ref: "wink"}}); err != nil {
		t.Fatal(err)
	}
	b.flush()
	got := s2.userMomentMap()["listening"]
	if len(got) != 1 || got[0].Ref != "wink" {
		t.Fatalf("s2's moment: %+v", got)
	}
	// Back to the defaults travels too.
	s2.setMoment("listening", nil)
	b.flush()
	if _, ok := s1.userMomentMap()["listening"]; ok {
		t.Fatal("reset did not go across")
	}
	if len(pix.userMomentMap()) != 0 {
		t.Fatal("a speaker without the display kind took a moment")
	}
	// Moments not shared stay local.
	s1.setMoment("thinking", []show{{Ref: "wink"}})
	b.flush()
	if _, ok := s2.userMomentMap()["thinking"]; ok {
		t.Fatal("an unshared moment went across")
	}
}

func TestWebSync(t *testing.T) {
	a, srv := newWebApp(t)
	_, out := call(t, "POST", srv.URL+"/api/login", "", `{"user":"admin","password":"yastation"}`)
	tok := out["token"].(string)
	if code, out := call(t, "GET", srv.URL+"/api/sync", tok, ""); code != 200 || out["connected"] != false {
		t.Fatalf("get without a broker: %d %v", code, out)
	}
	if code, _ := call(t, "POST", srv.URL+"/api/sync", tok, `{"kind":"setting","item":"volume_step","on":true}`); code != 503 {
		t.Fatalf("share without a broker: %d", code)
	}
	b := newBroker()
	b.attach(a)
	if code, _ := call(t, "POST", srv.URL+"/api/sync", tok, `{"kind":"setting","item":"device_id","on":true}`); code != 422 {
		t.Fatalf("a speaker's own key: %d", code)
	}
	code, out := call(t, "POST", srv.URL+"/api/sync", tok, `{"kind":"setting","item":"volume_step","on":true}`)
	if code != 200 || out["settings"].(map[string]any)["volume_step"] == nil {
		t.Fatalf("share: %d %v", code, out)
	}
	b.flush()
	var m sharedMsg
	json.Unmarshal([]byte(b.retained["yastation/shared/setting/volume_step"]), &m)
	if string(m.V) != `null` || m.By != "t1" { // at the default: shared as the default
		t.Fatalf("retained: %+v", m)
	}
	code, _ = call(t, "POST", srv.URL+"/api/sync", tok, `{"kind":"setting","item":"volume_step","on":false}`)
	b.flush()
	if _, ok := b.retained["yastation/shared/setting/volume_step"]; code != 200 || ok {
		t.Fatal("unshare clears the retained topic")
	}
}

// A shared restart key whose trial fails here is not taken again on the next connect (that
// would restart the speaker in a loop), until the item changes.
func TestSyncFailedTrialNotRetaken(t *testing.T) {
	b := newBroker()
	s1, s2 := speaker(t, "s1"), speaker(t, "s2")
	s1.quit, s2.quit = func() {}, func() {}
	b.attach(s1)
	b.attach(s2)
	s1.publishShared("setting", "wake_enabled", true)
	b.flush()
	setKey(t, s1, "wake_enabled", "false")
	b.flush()
	if _, err := os.Stat(s2.trialMarker()); err != nil {
		t.Fatal("s2 should be on trial")
	}
	// s2's trial fails: the old config comes back, the daemon restarts.
	s2.trialFailed("test")
	text, _ := os.ReadFile(s2.cfg().File)
	s2.adoptConfig(text)
	s2.restartPending.Store(false)
	s2.loadSyncRejected()
	os.Remove(s2.cfg().File + ".bak")
	b.attach(s2) // reconnects: the same retained value
	if _, err := os.Stat(s2.trialMarker()); err == nil {
		t.Fatal("took the rejected value again")
	}
	if v := s2.syncViewNow(); len(v.Rejected) != 1 || v.Rejected[0] != "setting/wake_enabled" {
		t.Fatalf("rejected: %v", v.Rejected)
	}
	// A new change of the item is tried again.
	setKey(t, s1, "wake_enabled", "true")
	text, _ = os.ReadFile(s1.cfg().File)
	s1.adoptConfig(text)
	setKey(t, s1, "wake_enabled", "false")
	b.flush()
	if _, err := os.Stat(s2.trialMarker()); err != nil {
		t.Fatal("a later change was not tried")
	}
	// Its trial passes: nothing stays pending.
	s2.syncAccepted()
	if _, err := os.Stat(s2.cfwFile(syncPendingFile)); err == nil {
		t.Fatal("pending kept after acceptance")
	}
}

// A setting left at the default is shared as null: every speaker keeps (or goes back to) its own
// build's default, so a newer default reaches them all; a value shared before the default changed
// that equals this build's default counts as the default too.
func TestSyncDefaultsStayDefaults(t *testing.T) {
	b := newBroker()
	s1, s2 := speaker(t, "s1"), speaker(t, "s2")
	b.attach(s1)
	b.attach(s2)
	setKey(t, s2, "volume_step", "4")
	s1.publishShared("setting", "volume_step", true) // s1 is at the default
	b.flush()
	if got := b.retained["yastation/shared/setting/volume_step"]; !strings.Contains(got, `"v":null`) {
		t.Fatalf("retained %q", got)
	}
	f, _ := os.ReadFile(s2.cfg().File)
	if s2.cfg().VolumeStep != 1 || s2.cfg().Has("volume_step") || strings.Contains(string(f), "volume_step") {
		t.Fatalf("s2 kept its override: %d %q", s2.cfg().VolumeStep, f)
	}

	// An override goes out as a value; Reset (the key leaves the file) as null again.
	setKey(t, s1, "volume_step", "3")
	b.flush()
	if s2.cfg().VolumeStep != 3 {
		t.Fatalf("override not taken: %d", s2.cfg().VolumeStep)
	}
	old, _ := os.ReadFile(s1.cfg().File)
	if _, _, err := s1.applyConfigText([]byte(strings.ReplaceAll(string(old), "volume_step = 3\n", "")), "test"); err != nil {
		t.Fatal(err)
	}
	b.flush()
	if s2.cfg().Has("volume_step") || s2.cfg().VolumeStep != 1 {
		t.Fatalf("reset not taken: %d", s2.cfg().VolumeStep)
	}

	// Set explicitly to the default value: still the default, nothing pinned.
	setKey(t, s1, "volume_step", "1")
	b.flush()
	if s2.cfg().Has("volume_step") {
		t.Fatal("an explicit default pinned the key")
	}

	// An old retained value equal to this build's default (shared before it changed): not written.
	s3 := speaker(t, "s3")
	s3.onShared("setting", "volume_step", []byte(`{"v":["1"],"at":1,"by":"old"}`))
	if s3.cfg().Has("volume_step") {
		t.Fatal("an old default-valued share was written into the file")
	}
	s3.onShared("setting", "volume_step", []byte(`{"v":["2"],"at":2,"by":"old"}`))
	if s3.cfg().VolumeStep != 2 {
		t.Fatalf("a real override not taken: %d", s3.cfg().VolumeStep)
	}
}
