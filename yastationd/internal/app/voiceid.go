package app

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/voiceid"
	"github.com/celebeing/yastationd/yastationd/internal/wake"
)

// Voice ID on the speaker (docs/voice-id.md). Every wake word starts a voiceprint of the voice
// that said it: «Алиса» itself (the audio from wakeWordLead before the wake point), then the
// request. With guests ignored the request waits for an early verdict (voice_hold_ms of it, held
// and sent at once after), and a guest's never reaches HA: the voice_guest moment shows that it
// was heard and ignored. Otherwise the request goes on at once and the verdict follows. Either way
// the verdict on the whole request goes to HA's sensors and, with the transcript, to the store.

const voicePrintWait = 3 * time.Second // the longest a voiceprint may take

type voiceID struct {
	host   *voiceid.Host
	gender *voiceid.Classifier
	age    *voiceid.Classifier

	mu     sync.Mutex
	people  []voiceid.Person // the household, from the store (VoicePeople, retained)
	storeID string           // the speaker that keeps the voices
	spool  []string         // events waiting for the broker
}

// voiceSpoolMax bounds the events kept while the broker is away (a few KB each).
const voiceSpoolMax = 200

// voiceModelDim: the voiceprint model spotter-host runs (the 512-dim next_tdnn of Mini 3 0.369,
// Mini 3 Pro and Station 3; the old Mini 3 firmware's 256-dim one is not taken).
const voiceModelDim = 512

// voiceBioDir: the voice models in use ("" when none fits; old: a dir with the old model).
func (a *App) voiceBioDir() (dir, old string) {
	c := a.cfg()
	for _, d := range []string{c.VoiceBio, a.prof.Platform.CFWRoot + "/vendor/bio_lingware", "/system/vendor/quasar/bio_lingware"} {
		if d == "" {
			continue
		}
		b, err := os.ReadFile(c.Path(d + "/tflite-online-ecapa-tdnn-frontend/config.json"))
		if err != nil {
			continue
		}
		var fc struct {
			Dim int `json:"embedding-dim"`
		}
		if json.Unmarshal(b, &fc) == nil && fc.Dim == voiceModelDim {
			return d, ""
		}
		old = d
	}
	return "", old
}

// newVoiceID sets voice ID up when it is on and the models are here (nil otherwise).
func (a *App) newVoiceID() *voiceID {
	c := a.cfg()
	if !c.VoiceID {
		return nil
	}
	dir, old := a.voiceBioDir()
	if dir == "" {
		if old != "" {
			a.log.Warn("voice id: off, this firmware's voice models are the old ones: put the newer ones in <cfw>/vendor/bio_lingware (make voice-models)", "old", old)
		} else {
			a.log.Warn("voice id: off, no voice models (voice_bio_dir, <cfw>/vendor/bio_lingware or the firmware's)")
		}
		return nil
	}
	if _, err := os.Stat(c.Path(c.SpotterBin)); err != nil {
		a.log.Warn("voice id: off, spotter-host missing", "path", c.SpotterBin)
		return nil
	}
	v := &voiceID{host: &voiceid.Host{
		Bin: c.Path(c.SpotterBin),
		Dir: c.Path(dir),
		Env: []string{"LD_LIBRARY_PATH=" + a.prof.Wake.Libs},
		Log: a.log,
	}}
	// The classifiers are optional: the old Mini 3 firmware's models have none.
	var err error
	if v.gender, err = voiceid.LoadClassifier(c.Path(dir + "/tflite-linear-gender-classifier/gender_classifier.tflite")); err != nil {
		a.log.Info("voice id: no gender classifier", "err", err)
	}
	if v.age, err = voiceid.LoadClassifier(c.Path(dir + "/tflite-linear-age-classifier/age_classifier.tflite")); err != nil {
		a.log.Info("voice id: no age classifier", "err", err)
	}
	a.log.Info("voice id: on", "models", dir, "store", c.VoiceStore)
	go func() { // load the models now: the first wake word should not wait for them
		if u, err := v.host.Begin(); err == nil {
			u.End()
		}
	}()
	return v
}

func (v *voiceID) household() []voiceid.Person {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.people
}

// onVoicePeople takes the household's voices from the store (retained).
func (a *App) onVoicePeople(payload []byte) {
	var p struct {
		Model  string           `json:"model"`
		Store  string           `json:"store"`
		People []voiceid.Person `json:"people"`
	}
	if len(payload) > 0 && json.Unmarshal(payload, &p) != nil {
		a.log.Warn("voice id: bad people message")
		return
	}
	if tag := a.vid.host.Tag(); p.Model != "" && tag != "" && p.Model != tag {
		a.log.Warn("voice id: the store's voices are from another model, not used", "theirs", p.Model, "ours", tag)
		p.People = nil
	}
	a.vid.mu.Lock()
	a.vid.people, a.vid.storeID = p.People, p.Store
	a.vid.mu.Unlock()
	a.log.Info("voice id: household", "people", len(p.People))
}

// voiceTake is one voiceprint in the making: the utterance and the audio feeding it.
type voiceTake struct {
	u       *voiceid.Utterance
	unsub   func()
	wake    time.Time // when the wake word was heard (or the session started)
	src     string
	speaker string
	once    sync.Once
	feed    sync.Once

	mu         sync.Mutex
	early      *voiceid.Verdict
	transcript string
}

// voiceTakeAt starts a voiceprint at a wake word (ev) or, with ev nil, at the start of a session.
// nil when voice ID is off or the voiceprint host can't run.
func (a *App) voiceTakeAt(ev *wake.Event, src string) *voiceTake {
	if a.vid == nil {
		return nil
	}
	u, err := a.vid.host.Begin()
	if err != nil {
		a.log.Warn("voice id: no voiceprint", "err", err)
		return nil
	}
	t := &voiceTake{u: u, wake: time.Now(), src: src, speaker: a.cfg().DeviceID}
	if ev != nil && a.spot != nil && a.spot.Running() {
		// «Алиса» itself, whatever wake_word_in_stream sends to HA: the most comparable sample.
		t.unsub, _ = a.spot.SubscribeVQEAt(*ev, wakeWordLead, u.Feed)
	} else {
		t.unsub = a.wakeAudio(nil, u.Feed)
	}
	return t
}

// end stops feeding and abandons the voiceprint (idempotent; a nil take is fine).
func (t *voiceTake) end() {
	if t == nil {
		return
	}
	t.once.Do(func() {
		t.stopFeed()
		t.u.End()
	})
}

// stopFeed: no more audio for the voiceprint.
func (t *voiceTake) stopFeed() { t.feed.Do(t.unsub) }

// verdict takes the voiceprint of what was fed so far.
func (a *App) voiceVerdict(t *voiceTake, stage string) voiceid.Verdict {
	p := t.u.Print(voicePrintWait)
	c := a.cfg()
	v := voiceid.Match(p.Emb, a.vid.household(), c.GuestThreshold, c.GuestMargin)
	v.Secs, v.Stage, v.Speaker, v.Model = p.Secs, stage, t.speaker, a.vid.host.Tag()
	if p.Err != nil {
		a.log.Warn("voice id: voiceprint failed", "err", p.Err)
	}
	if p.Emb != nil {
		if a.vid.gender != nil {
			v.Female = round3(a.vid.gender.Predict(p.Emb))
		}
		if a.vid.age != nil {
			v.Age = round3(a.vid.age.Predict(p.Emb))
		}
		v.Gender = voiceid.GenderOf(v.Female)
	}
	return v
}

func round3(x float64) float64 { return math.Round(x*1000) / 1000 }

// ignoringGuests: requests from guests are dropped (the option is on and someone is enrolled).
func (a *App) ignoringGuests() bool {
	return a.vid != nil && a.store.get().VoiceIgnoreGuests && len(a.vid.household()) > 0
}

// voiceGate decides a wake word's request with guests ignored: after voice_hold_ms of it the
// early verdict lets the held speech go to HA (go) or drops it. Without the gate go runs at once
// and the early verdict only feeds the sensors.
func (a *App) voiceGate(held *heldWake, start func()) {
	t := held.take
	if t == nil {
		start()
		return
	}
	gate := a.ignoringGuests()
	if !gate {
		start()
	} else {
		a.moment("voice", "listening", stageTTL) // the check is under way; the speech is held
	}
	go func() {
		if d := a.cfg().VoiceHold - time.Since(t.wake); d > 0 {
			time.Sleep(d)
		}
		v := a.voiceVerdict(t, "early")
		t.mu.Lock()
		t.early = &v
		t.mu.Unlock()
		a.log.Info("voice id: early verdict", "state", v.State, "name", v.Name, "score", v.Score,
			"closest", v.Closest, "gender", v.Gender, "secs", v.Secs, "gate", gate)
		a.publishVerdict(v)
		if !gate {
			return
		}
		if v.State == voiceid.Guest || (v.State == voiceid.Unknown && a.cfg().VoiceUnsure == "drop") {
			held.drop()
			a.log.Info("voice id: request ignored", "state", v.State, "score", v.Score)
			a.unexpress("voice")
			a.hook("voice_guest")
			a.voiceSend(t, v, true)
			return
		}
		start()
	}()
}

// voiceFinal takes the verdict on the whole request when its session ends.
func (a *App) voiceFinal(t *voiceTake) {
	if t == nil {
		return
	}
	t.stopFeed() // nothing more to hear; the utterance stays for its voiceprint
	go func() {
		v := a.voiceVerdict(t, "final")
		t.end()
		a.log.Info("voice id: verdict", "state", v.State, "name", v.Name, "score", v.Score,
			"closest", v.Closest, "second", v.Second, "gender", v.Gender, "female", v.Female, "secs", v.Secs)
		a.publishVerdict(v)
		a.voiceSend(t, v, false)
	}()
}

// noteTranscript keeps HA's transcript for the store's log.
func (t *voiceTake) noteTranscript(text string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.transcript = text
	t.mu.Unlock()
}

// publishVerdict updates HA's voice sensors.
func (a *App) publishVerdict(v voiceid.Verdict) {
	state := v.State
	if v.State == voiceid.Known {
		state = v.Name
	}
	attr, _ := json.Marshal(v)
	a.publish(a.topics.State("voice_attr"), string(attr))
	a.publish(a.topics.State("voice"), state)
	a.publish(a.topics.State("voice_gender"), v.Gender)
	a.publish(a.topics.State("voice_known"), onOff(v.State == voiceid.Known))
}

// voiceEvent is what a speaker tells the store about one request.
type voiceEvent struct {
	ID         string          `json:"id"`
	At         int64           `json:"at"` // unix ms
	Speaker    string          `json:"speaker"`
	Src        string          `json:"src"`
	Transcript string          `json:"transcript,omitempty"`
	Ignored    bool            `json:"ignored,omitempty"`
	Verdict    voiceid.Verdict `json:"verdict"`
	Emb        string          `json:"emb,omitempty"` // base64 float32 LE
}

// voiceSend posts the request's voiceprint and verdict to the store (spooled while the broker is
// away). A request without a voiceprint is not sent.
func (a *App) voiceSend(t *voiceTake, v voiceid.Verdict, ignored bool) {
	if len(v.Embedding) == 0 {
		return
	}
	t.mu.Lock()
	tr := t.transcript
	t.mu.Unlock()
	ev := voiceEvent{ID: newEventID(), At: t.wake.UnixMilli(), Speaker: t.speaker, Src: t.src,
		Transcript: tr, Ignored: ignored, Verdict: v, Emb: packEmb(v.Embedding)}
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	if a.mqtt == nil && a.vstore != nil { // no broker at all: the store is here
		a.onVoiceEvent(b)
		return
	}
	a.vid.mu.Lock()
	a.vid.spool = append(a.vid.spool, string(b))
	if n := len(a.vid.spool); n > voiceSpoolMax {
		a.vid.spool = a.vid.spool[n-voiceSpoolMax:]
	}
	a.vid.mu.Unlock()
	a.voiceFlush()
}

// voiceFlush sends the spooled events while the broker is there.
func (a *App) voiceFlush() {
	if a.vid == nil || a.mqtt == nil || !a.mqtt.Connected() {
		return
	}
	a.vid.mu.Lock()
	out := a.vid.spool
	a.vid.spool = nil
	a.vid.mu.Unlock()
	for _, e := range out {
		a.mqtt.PublishEvent(a.topics.VoiceEvents(), e)
	}
}

func parseOnOff(p []byte) bool { return strings.EqualFold(strings.TrimSpace(string(p)), "ON") }

// newEventID: a random id for a store event.
func newEventID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func packEmb(e []float32) string {
	b := make([]byte, 4*len(e))
	for i, f := range e {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(f))
	}
	return base64.StdEncoding.EncodeToString(b)
}

func unpackEmb(s string) []float32 {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(b)%4 != 0 {
		return nil
	}
	e := make([]float32, len(b)/4)
	for i := range e {
		e[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return e
}

// cmdVoiceIgnoreGuests: the HA switch (and the web app's option).
func (a *App) cmdVoiceIgnoreGuests(payload []byte) {
	on := parseOnOff(payload)
	a.store.update(func(s *persistState) { s.VoiceIgnoreGuests = on })
	a.publish(a.topics.State("voice_ignore_guests"), onOff(on))
}
