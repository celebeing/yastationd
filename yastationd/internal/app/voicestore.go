package app

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/voiceid"
)

// The voice store (docs/voice-id.md, "One store, two one-way streams"): on the speaker with
// voice_store, the log of every speaker's requests (voiceprint, transcript, verdict) and the album
// that groups them into people. Speakers post events (voiceid.go); the store publishes the named
// people's centroids, retained, for every speaker to classify against. Only the store edits.
//
// Files under <cfw>/voices: log.jsonl (one entry per line, appended; rewritten when entries go)
// and edits.json (the owner's names and pins).

const (
	voiceStoreMax  = 3000 // entries kept (~3 KB each on flash)
	voicePublishIn = 2 * time.Second
)

type voiceStore struct {
	mu    sync.Mutex
	dir   string
	al    *voiceid.Album
	model string // the voiceprint model of the newest entry
	pub   *time.Timer
}

// storedEntry is a log line: the entry and its voiceprint.
type storedEntry struct {
	voiceid.Entry
	Emb string `json:"emb"`
}

// newVoiceStore loads the store when this speaker keeps the voices (nil otherwise).
func (a *App) newVoiceStore() *voiceStore {
	if a.vid == nil || !a.cfg().VoiceStore {
		return nil
	}
	st := &voiceStore{dir: a.cfwFile("voices"), al: voiceid.NewAlbum()}
	if err := os.MkdirAll(st.dir, 0o700); err != nil {
		a.log.Warn("voice store: off", "err", err)
		return nil
	}
	if b, err := os.ReadFile(filepath.Join(st.dir, "edits.json")); err == nil {
		var e voiceid.Edits
		if json.Unmarshal(b, &e) == nil {
			if e.Names != nil {
				st.al.Edits.Names = e.Names
			}
			if e.Pins != nil {
				st.al.Edits.Pins = e.Pins
			}
			if e.Home != nil {
				st.al.Edits.Home = e.Home
			} else { // edits from before the household mark: a name made one
				for id := range st.al.Edits.Names {
					st.al.Edits.Home[id] = true
				}
			}
		}
	}
	if f, err := os.Open(filepath.Join(st.dir, "log.jsonl")); err == nil {
		sc := bufio.NewScanner(f)
		sc.Buffer(nil, 1<<20)
		for sc.Scan() {
			var se storedEntry
			if json.Unmarshal(sc.Bytes(), &se) != nil || se.ID == "" {
				continue
			}
			e := se.Entry
			e.Emb = unpackEmb(se.Emb)
			st.al.Entries = append(st.al.Entries, &e)
			if e.Model != "" {
				st.model = e.Model
			}
		}
		f.Close()
	}
	st.al.Regroup()
	a.log.Info("voice store: on", "entries", len(st.al.Entries), "people", len(st.al.People()))
	return st
}

// onVoiceEvent files a speaker's event.
func (a *App) onVoiceEvent(payload []byte) {
	st := a.vstore
	var ev voiceEvent
	if json.Unmarshal(payload, &ev) != nil || ev.ID == "" {
		return
	}
	e := &voiceid.Entry{ID: ev.ID, At: ev.At, Speaker: ev.Speaker, Src: ev.Src, Transcript: ev.Transcript,
		Ignored: ev.Ignored, Secs: ev.Verdict.Secs, Female: ev.Verdict.Female, Age: ev.Verdict.Age,
		Model: ev.Verdict.Model, Score: ev.Verdict.Score, Verdict: ev.Verdict.State, Emb: unpackEmb(ev.Emb)}
	if e.Verdict == voiceid.Known {
		e.Verdict = ev.Verdict.Name
	}
	if x := ev.Early; x != nil {
		e.Early, e.EarlyScore, e.EarlySecs = x.State, x.Score, x.Secs
		if x.State == voiceid.Known {
			e.Early = x.Name
		}
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.model != "" && e.Model != "" && e.Model != st.model {
		a.log.Warn("voice store: an event from another voiceprint model, not filed", "speaker", e.Speaker, "model", e.Model)
		return
	}
	if !st.al.Add(e) {
		return
	}
	if e.Model != "" {
		st.model = e.Model
	}
	if st.al.Trim(voiceStoreMax) {
		st.writeLog(a)
	} else {
		st.appendLog(a, e)
	}
	a.voicePublishSoon()
}

func entryLine(e *voiceid.Entry) []byte {
	b, _ := json.Marshal(storedEntry{Entry: *e, Emb: packEmb(e.Emb)})
	return append(b, '\n')
}

func (st *voiceStore) appendLog(a *App, e *voiceid.Entry) {
	f, err := os.OpenFile(filepath.Join(st.dir, "log.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		a.log.Warn("voice store: log", "err", err)
		return
	}
	f.Write(entryLine(e))
	f.Close()
}

// writeLog rewrites the log from the album (entries gone).
func (st *voiceStore) writeLog(a *App) {
	var b []byte
	for _, e := range st.al.Entries {
		b = append(b, entryLine(e)...)
	}
	if err := writeFileAtomic(filepath.Join(st.dir, "log.jsonl"), b, 0o600); err != nil {
		a.log.Warn("voice store: log", "err", err)
	}
}

func (st *voiceStore) writeEdits(a *App) {
	b, _ := json.MarshalIndent(st.al.Edits, "", " ")
	if err := writeFileAtomic(filepath.Join(st.dir, "edits.json"), b, 0o600); err != nil {
		a.log.Warn("voice store: edits", "err", err)
	}
}

// voicePublishSoon publishes the people after a short quiet spell (events come in bursts).
func (a *App) voicePublishSoon() {
	st := a.vstore
	if st.pub != nil {
		st.pub.Stop()
	}
	st.pub = time.AfterFunc(voicePublishIn, a.voicePublish)
}

// voicePublish sends the named people's centroids to every speaker (retained).
func (a *App) voicePublish() {
	st := a.vstore
	st.mu.Lock()
	msg := map[string]any{"model": st.model, "store": a.cfg().DeviceID, "people": st.al.People()}
	st.mu.Unlock()
	if msg["people"] == nil {
		msg["people"] = []voiceid.Person{}
	}
	b, _ := json.Marshal(msg)
	if a.mqtt != nil {
		a.mqtt.Publish(a.topics.VoicePeople(), string(b))
	}
	a.onVoicePeople(b) // this speaker too, broker or not
}

func writeFileAtomic(path string, b []byte, mode os.FileMode) error {
	if err := os.WriteFile(path+".tmp", b, mode); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

// readJSON decodes a small request body into v, answering 400 itself when it can't.
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "bad JSON: "+err.Error())
		return false
	}
	return true
}

// --- the Voices tab's API ---

type voiceEntryView struct {
	*voiceid.Entry
	Group string `json:"group"`
	Pin   bool   `json:"pinned,omitempty"`
}

// apiVoices: GET /api/voices (the groups, or where the store is), /api/voices/log, /api/voices/preview;
// POST /api/voices/groups/<id> {name?, household?}, /api/voices/merge {from, into}, /api/voices/entries/<id> {group};
// DELETE /api/voices/groups/<id> (forget a person), /api/voices (forget everything).
func (a *App) apiVoices(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/voices"), "/")
	st := a.vstore
	if st == nil {
		if r.Method != http.MethodGet || rest != "" {
			writeErr(w, http.StatusNotFound, "the voices are kept on another speaker")
			return
		}
		writeJSON(w, http.StatusOK, a.voiceStatus())
		return
	}
	switch {
	case rest == "" && r.Method == http.MethodGet:
		st.mu.Lock()
		out := a.voiceStatus()
		out["groups"] = st.al.Groups()
		out["entries"] = len(st.al.Entries)
		st.mu.Unlock()
		writeJSON(w, http.StatusOK, out)
	case rest == "" && r.Method == http.MethodDelete:
		st.mu.Lock()
		st.al = voiceid.NewAlbum()
		st.writeLog(a)
		st.writeEdits(a)
		st.mu.Unlock()
		a.log.Info("voice store: everything forgotten")
		a.voicePublish()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case rest == "log" && r.Method == http.MethodGet:
		a.apiVoiceLog(w, r)
	case rest == "preview" && r.Method == http.MethodGet:
		a.apiVoicePreview(w, r)
	case rest == "merge" && r.Method == http.MethodPost:
		var req struct{ From, Into string }
		if !readJSON(w, r, &req) {
			return
		}
		a.voiceEdit(w, func(al *voiceid.Album) error { return al.Merge(req.From, req.Into) })
	case strings.HasPrefix(rest, "groups/"):
		id := strings.TrimPrefix(rest, "groups/")
		switch r.Method {
		case http.MethodPost: // {name} and/or {household}
			var req struct {
				Name      *string
				Household *bool
			}
			if !readJSON(w, r, &req) {
				return
			}
			a.voiceEdit(w, func(al *voiceid.Album) error {
				if req.Name != nil {
					if err := al.Rename(id, strings.TrimSpace(*req.Name)); err != nil {
						return err
					}
				}
				if req.Household != nil {
					return al.SetHousehold(id, *req.Household)
				}
				return nil
			})
		case http.MethodDelete:
			st.mu.Lock()
			n := st.al.Forget(id)
			st.writeLog(a)
			st.writeEdits(a)
			st.mu.Unlock()
			a.log.Info("voice store: a voice forgotten", "group", id, "entries", n)
			a.voicePublish()
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "forgotten": n})
		default:
			writeErr(w, http.StatusMethodNotAllowed, "POST or DELETE")
		}
	case strings.HasPrefix(rest, "entries/") && r.Method == http.MethodPost:
		id := strings.TrimPrefix(rest, "entries/")
		var req struct{ Group string }
		if !readJSON(w, r, &req) {
			return
		}
		a.voiceEdit(w, func(al *voiceid.Album) error { _, err := al.Move(id, req.Group); return err })
	default:
		writeErr(w, http.StatusNotFound, "no such voices call")
	}
}

// voiceStatus: what every speaker can say about voice ID.
func (a *App) voiceStatus() map[string]any {
	out := map[string]any{"on": a.vid != nil, "store": a.vstore != nil, "threshold": a.cfg().GuestThreshold}
	if a.vid != nil {
		out["model"] = a.vid.host.Tag()
		out["people"] = len(a.vid.household())
		out["ignore_guests"] = a.store.get().VoiceIgnoreGuests
		a.vid.mu.Lock()
		out["store_id"] = a.vid.storeID
		a.vid.mu.Unlock()
	}
	return out
}

// voiceEdit applies an owner's edit, saves it and republishes the people.
func (a *App) voiceEdit(w http.ResponseWriter, fn func(*voiceid.Album) error) {
	st := a.vstore
	st.mu.Lock()
	err := fn(st.al)
	if err == nil {
		st.writeEdits(a)
	}
	st.mu.Unlock()
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	a.voicePublish()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// apiVoiceLog: the newest entries first (?group= one group's, ?limit= how many, default 100).
func (a *App) apiVoiceLog(w http.ResponseWriter, r *http.Request) {
	st := a.vstore
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > voiceStoreMax {
		limit = 100
	}
	group := r.URL.Query().Get("group")
	st.mu.Lock()
	defer st.mu.Unlock()
	out := []voiceEntryView{}
	for i := len(st.al.Entries) - 1; i >= 0 && len(out) < limit; i-- {
		e := st.al.Entries[i]
		g := st.al.GroupOf(e.ID)
		if group != "" && g != group {
			continue
		}
		_, pin := st.al.Edits.Pins[e.ID]
		out = append(out, voiceEntryView{Entry: e, Group: g, Pin: pin})
	}
	writeJSON(w, http.StatusOK, out)
}

// apiVoicePreview: what a threshold (?threshold=, default the current one) would have done to the
// last requests of real speech: each one's score against the named people, and which would have
// been taken for a guest.
func (a *App) apiVoicePreview(w http.ResponseWriter, r *http.Request) {
	st := a.vstore
	th, err := strconv.ParseFloat(r.URL.Query().Get("threshold"), 64)
	if err != nil {
		th = a.cfg().GuestThreshold
	}
	type row struct {
		ID         string  `json:"id"`
		At         int64   `json:"at"`
		Speaker    string  `json:"speaker"`
		Transcript string  `json:"transcript,omitempty"`
		Group      string  `json:"group"`
		Score      float64 `json:"score"`
		Closest    string  `json:"closest,omitempty"`
		State      string  `json:"state"`
		Name       string  `json:"name,omitempty"`
	}
	st.mu.Lock()
	people := st.al.People()
	rows := []row{}
	for i := len(st.al.Entries) - 1; i >= 0 && len(rows) < 100; i-- {
		e := st.al.Entries[i]
		g := st.al.GroupOf(e.ID)
		if g == voiceid.Noise || len(e.Emb) == 0 {
			continue
		}
		v := voiceid.Match(e.Emb, people, th, a.cfg().GuestMargin)
		rows = append(rows, row{ID: e.ID, At: e.At, Speaker: e.Speaker, Transcript: e.Transcript, Group: g,
			Score: v.Score, Closest: v.Closest, State: v.State, Name: v.Name})
	}
	st.mu.Unlock()
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].At > rows[j].At })
	guests := 0
	for _, r := range rows {
		if r.State == voiceid.Guest {
			guests++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"threshold": th, "people": len(people), "rows": rows, "guests": guests})
}
