package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/config"
	"github.com/celebeing/yastationd/yastationd/internal/voiceid"
)

// fakeVoiceHost answers every voiceprint request with $EMB (a stand-in for `spotter-host voice`).
const fakeVoiceHost = `#!/bin/sh
echo "READY test_tag"
while read -r c arg; do
  case $c in
  a) head -c "$arg" >/dev/null ;;
  v) echo "VOICE $arg 2.00 $EMB" ;;
  esac
done
`

func voiceTestApp(t *testing.T, emb string) *App {
	a := newExpressApp(t)
	bin := filepath.Join(t.TempDir(), "spotter-host")
	if err := os.WriteFile(bin, []byte(fakeVoiceHost), 0o755); err != nil {
		t.Fatal(err)
	}
	a.vid = &voiceID{host: &voiceid.Host{Bin: bin, Dir: "/x", Env: []string{"EMB=" + emb}, Log: a.log}}
	a.vid.people = []voiceid.Person{{ID: "g1", Name: "Петя", Centroid: []float32{1, 0, 0}}}
	a.store.update(func(s *persistState) { s.VoiceIgnoreGuests = true })
	a.sat = &satellite{a: a}
	return a
}

func gateTake(t *testing.T, a *App) *heldWake {
	u, err := a.vid.host.Begin()
	if err != nil {
		t.Fatal(err)
	}
	return &heldWake{take: &voiceTake{u: u, unsub: func() {}, wake: time.Now()}}
}

func TestVoiceGate(t *testing.T) {
	for _, tc := range []struct {
		name, emb string
		ignore    bool
		started   bool
	}{
		{"the household goes through", "0.9 0.1 0", true, true},
		{"a guest is dropped", "0 1 0", true, false},
		{"a guest goes through when guests are not ignored", "0 1 0", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := voiceTestApp(t, tc.emb)
			a.cfg().VoiceHold = 0
			a.store.update(func(s *persistState) { s.VoiceIgnoreGuests = tc.ignore })
			started := make(chan bool, 1)
			a.voiceGate(gateTake(t, a), func() { started <- true })
			select {
			case <-started:
				if !tc.started {
					t.Fatal("a guest's request went to HA")
				}
			case <-time.After(2 * time.Second):
				if tc.started {
					t.Fatal("the request never started")
				}
				if !a.matrixStack.Has("voice") {
					t.Error("no voice_guest moment for the ignored request")
				}
			}
			time.Sleep(50 * time.Millisecond)
			want := map[bool]string{true: "Петя", false: "guest"}[strings.HasPrefix(tc.emb, "0.9")]
			if got := a.states.snapshot()["voice"].value; got != want {
				t.Errorf("sensor %q, want %q", got, want)
			}
			var v voiceid.Verdict
			json.Unmarshal([]byte(a.states.snapshot()["voice_attr"].value), &v)
			if v.Stage != "early" || v.Secs != 2 {
				t.Errorf("attributes %+v", v)
			}
		})
	}
}

func TestVoiceGateNeedsSomeoneNamed(t *testing.T) {
	a := voiceTestApp(t, "0 1 0")
	a.vid.people = nil
	if a.ignoringGuests() {
		t.Fatal("with nobody named nothing can be a guest")
	}
}

// The store files events, groups them, and a name makes a person every speaker knows.
func TestVoiceStoreNamesAPerson(t *testing.T) {
	a := voiceTestApp(t, "1 0 0")
	a.vid.people = nil
	c := config.Default()
	c.SysRoot, c.VoiceStore = t.TempDir(), true
	a.conf.Store(&c)
	a.vstore = a.newVoiceStore()
	if a.vstore == nil {
		t.Fatal("no store")
	}
	for i, e := range [][]float32{{1, 0.1, 0}, {0.9, 0, 0.1}, {1, 0, 0.05}, {0, 1, 0}} {
		ev := voiceEvent{ID: "e" + string(rune('0'+i)), At: int64(i), Speaker: "s1", Transcript: "свет",
			Verdict: voiceid.Verdict{State: voiceid.Unknown, Secs: 2, Female: 0.1, Model: "test_tag"}, Emb: packEmb(e)}
		b, _ := json.Marshal(ev)
		a.onVoiceEvent(b)
	}
	groups := a.vstore.al.Groups()
	if len(groups) != 2 || groups[0].Count != 3 { // Петя ×3 and someone else (the empty noise group is not listed)
		t.Fatalf("groups %+v", groups)
	}
	a.vstore.mu.Lock()
	a.vstore.al.Rename(groups[0].ID, "Петя")
	a.vstore.writeEdits(a)
	a.vstore.mu.Unlock()
	a.voicePublish()
	if p := a.vid.household(); len(p) != 1 || p[0].Name != "Петя" {
		t.Fatalf("household %+v", p)
	}
	// it survives a restart: the log and the edits are on flash
	st := a.newVoiceStore()
	if len(st.al.Entries) != 4 || len(st.al.People()) != 1 {
		t.Fatalf("reloaded %d entries, %d people", len(st.al.Entries), len(st.al.People()))
	}
}
