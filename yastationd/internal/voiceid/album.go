package voiceid

import (
	"fmt"
	"math"
	"sort"
)

// The album (docs/voice-id.md, "voices like faces in a photo album"): every request's voiceprint
// is an entry; similar entries group by themselves; the owner names groups, merges them and moves
// entries. Edits are pins (entry → group) the grouping never undoes: a named group's entries are
// pinned, so it keeps its members and its name, and later requests join it by similarity.

// Noise is the group of entries that are not a person (false wakes, noise, silence).
const Noise = "noise"

// JoinThreshold: an entry joins the closest group whose centroid is at least this close; else it
// starts a group of its own. The household's own voice scores 0.70-0.95 to its centroid in the
// desk check, other people under 0.55.
const JoinThreshold = 0.6

// Entry is one request in the album.
type Entry struct {
	ID         string  `json:"id"`
	At         int64   `json:"at"` // unix ms
	Speaker    string  `json:"speaker"`
	Src        string  `json:"src,omitempty"`
	Transcript string  `json:"transcript,omitempty"`
	Ignored    bool    `json:"ignored,omitempty"`
	Secs       float64 `json:"secs"`
	Female     float64 `json:"female"`
	Age        float64 `json:"age"`
	Model      string  `json:"model,omitempty"`
	Verdict    string  `json:"verdict,omitempty"` // what the speaker decided: a name, guest, unknown
	Score      float64 `json:"score"`
	// The early verdict («Алиса» and the start of the request), to see how well it agrees with the
	// final one (a name, guest, unknown; "" when there was none).
	Early      string    `json:"early,omitempty"`
	EarlyScore float64   `json:"early_score,omitempty"`
	EarlySecs  float64   `json:"early_secs,omitempty"`
	Emb        []float32 `json:"-"`
}

// Group is a set of entries the album thinks are one voice.
type Group struct {
	ID       string    `json:"id"`
	Name     string    `json:"name,omitempty"` // set by the owner: one of the household
	Noise    bool      `json:"noise,omitempty"`
	Count    int       `json:"count"`
	Female   float64   `json:"female"` // the members' mean (-1: no classifier)
	Gender   string    `json:"gender"`
	Last     int64     `json:"last"` // the newest member's time
	Speakers []string  `json:"speakers"`
	Centroid []float32 `json:"-"`
	sum      []float64
	nFemale  int
}

// Edits are the owner's: names of groups and pinned entries.
type Edits struct {
	Names map[string]string `json:"names"` // group → name
	Pins  map[string]string `json:"pins"`  // entry → group
}

// Album groups entries.
type Album struct {
	Entries []*Entry // oldest first
	Edits   Edits
	groupOf map[string]string // entry → group
	groups  map[string]*Group
}

// NewAlbum makes an empty album.
func NewAlbum() *Album {
	al := &Album{Edits: Edits{Names: map[string]string{}, Pins: map[string]string{}}}
	al.Regroup()
	return al
}

// isNoise: an entry that is not a person's request when nothing says otherwise: HA heard no words
// in it (a request that went to HA without a transcript), or almost no speech.
func isNoise(e *Entry) bool {
	if len(e.Emb) == 0 || e.Secs < 0.9 {
		return true
	}
	return e.Transcript == "" && !e.Ignored
}

// Regroup rebuilds every group from the entries, in time order: pins first, then the grouping.
func (al *Album) Regroup() {
	al.groupOf, al.groups = map[string]string{}, map[string]*Group{}
	al.group(Noise)
	for _, e := range al.Entries {
		al.place(e)
	}
	al.mergeClose()
}

// mergeClose joins groups whose centroids are as close as an entry must be to join one: the
// grouping goes entry by entry, so a voice whose first phrase was atypical can start two groups
// that only later turn out to be one (seen on a real household: 0.64 between the halves, ~0
// to the other voice). The closest pair goes first. Two named groups are never joined (that is
// the owner's call), and a group with pinned entries is never absorbed (an entry moved out on
// purpose stays out).
func (al *Album) mergeClose() {
	for {
		best, from, into := JoinThreshold, "", ""
		for aid, a := range al.groups {
			for bid, b := range al.groups {
				if aid >= bid || a.Noise || b.Noise || len(a.Centroid) == 0 || len(b.Centroid) == 0 {
					continue
				}
				if a.Name != "" && b.Name != "" {
					continue
				}
				c := Cosine(a.Centroid, b.Centroid)
				if c < best {
					continue
				}
				f, t := al.absorbable(a, b)
				if f == "" {
					continue
				}
				best, from, into = c, f, t
			}
		}
		if from == "" {
			return
		}
		for eid, gid := range al.groupOf {
			if gid == from {
				al.groupOf[eid] = into
			}
		}
		al.rebuild()
	}
}

// absorbable picks which of a and b goes into the other ("" if neither may): an unnamed group
// without pins goes into a named one, or the smaller of two such groups into the larger. A group
// the owner pinned but did not name (an entry moved to a new person) takes part in neither.
func (al *Album) absorbable(a, b *Group) (from, into string) {
	free := func(g *Group) bool { return g.Name == "" && !al.hasPins(g.ID) }
	switch {
	case a.Name != "" && free(b):
		return b.ID, a.ID
	case b.Name != "" && free(a):
		return a.ID, b.ID
	case free(a) && free(b):
		if a.Count < b.Count || (a.Count == b.Count && a.ID > b.ID) {
			return a.ID, b.ID
		}
		return b.ID, a.ID
	}
	return "", ""
}

func (al *Album) hasPins(gid string) bool {
	for _, g := range al.Edits.Pins {
		if g == gid {
			return true
		}
	}
	return false
}

// rebuild recomputes every group's stats from groupOf.
func (al *Album) rebuild() {
	of := al.groupOf
	al.groupOf, al.groups = map[string]string{}, map[string]*Group{}
	al.group(Noise)
	for _, e := range al.Entries {
		al.add(e, of[e.ID])
	}
}

func (al *Album) group(id string) *Group {
	g := al.groups[id]
	if g == nil {
		g = &Group{ID: id, Noise: id == Noise, Name: al.Edits.Names[id], Female: -1, Gender: Unknown, Speakers: []string{}}
		al.groups[id] = g
	}
	return g
}

// place puts e in its group: its pin, noise, the closest group, or a new one.
func (al *Album) place(e *Entry) {
	id, ok := al.Edits.Pins[e.ID]
	if !ok {
		switch {
		case isNoise(e):
			id = Noise
		default:
			best, bid := -2.0, ""
			for gid, g := range al.groups {
				if g.Noise || len(g.Centroid) == 0 {
					continue
				}
				if c := Cosine(e.Emb, g.Centroid); c > best || (c == best && gid < bid) {
					best, bid = c, gid
				}
			}
			if bid != "" && best >= JoinThreshold {
				id = bid
			} else {
				id = "g-" + e.ID
			}
		}
	}
	al.add(e, id)
}

// add counts e into group id.
func (al *Album) add(e *Entry, id string) {
	g := al.group(id)
	al.groupOf[e.ID] = id
	g.Count++
	g.Last = max(g.Last, e.At)
	if !contains(g.Speakers, e.Speaker) {
		g.Speakers = append(g.Speakers, e.Speaker)
		sort.Strings(g.Speakers)
	}
	if !g.Noise && len(e.Emb) > 0 {
		addTo(g, e)
	}
}

// addTo adds e's normalised voiceprint to g's centroid and its gender to the mean.
func addTo(g *Group, e *Entry) {
	var n float64
	for _, v := range e.Emb {
		n += float64(v) * float64(v)
	}
	if n == 0 {
		return
	}
	n = math.Sqrt(n)
	if g.sum == nil {
		g.sum = make([]float64, len(e.Emb))
	}
	if len(g.sum) != len(e.Emb) {
		return
	}
	for i, v := range e.Emb {
		g.sum[i] += float64(v) / n
	}
	g.Centroid = make([]float32, len(g.sum))
	for i, v := range g.sum {
		g.Centroid[i] = float32(v)
	}
	if e.Female >= 0 {
		g.nFemale++
		if g.Female < 0 {
			g.Female = e.Female
		} else {
			g.Female += (e.Female - g.Female) / float64(g.nFemale)
		}
	}
	g.Gender = GenderOf(g.Female)
}

// Add takes a new entry (or ignores a known id).
func (al *Album) Add(e *Entry) bool {
	if _, ok := al.groupOf[e.ID]; ok {
		return false
	}
	al.Entries = append(al.Entries, e)
	al.place(e)
	al.mergeClose()
	return true
}

// GroupOf is the entry's group id.
func (al *Album) GroupOf(id string) string { return al.groupOf[id] }

// Groups lists the groups: named first, then by size; noise last.
func (al *Album) Groups() []*Group {
	out := make([]*Group, 0, len(al.groups))
	for _, g := range al.groups {
		if g.Count > 0 || g.Name != "" {
			if g.Gender == "" {
				g.Gender = Unknown
			}
			out = append(out, g)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Noise != b.Noise {
			return b.Noise
		}
		if (a.Name != "") != (b.Name != "") {
			return a.Name != ""
		}
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.ID < b.ID
	})
	return out
}

// People are the named groups as the speakers need them.
func (al *Album) People() []Person {
	var out []Person
	for _, g := range al.Groups() {
		if g.Name != "" && !g.Noise && len(g.Centroid) > 0 {
			out = append(out, Person{ID: g.ID, Name: g.Name, Gender: g.Gender, Centroid: g.Centroid})
		}
	}
	return out
}

// pinGroup pins every entry now in group id to it (a named group keeps its members).
func (al *Album) pinGroup(id string) {
	for eid, gid := range al.groupOf {
		if gid == id {
			al.Edits.Pins[eid] = id
		}
	}
}

// Rename names group id (empty: forget the name, the entries stay pinned).
func (al *Album) Rename(id, name string) error {
	g := al.groups[id]
	if g == nil || g.Noise {
		return fmt.Errorf("no group %s", id)
	}
	al.pinGroup(id)
	if name == "" {
		delete(al.Edits.Names, id)
	} else {
		al.Edits.Names[id] = name
	}
	al.Regroup()
	return nil
}

// Merge moves every entry of group from into group into.
func (al *Album) Merge(from, into string) error {
	if al.groups[from] == nil || al.groups[into] == nil || from == into {
		return fmt.Errorf("can't merge %s into %s", from, into)
	}
	for eid, gid := range al.groupOf {
		if gid == from {
			al.Edits.Pins[eid] = into
		}
	}
	al.pinGroup(into)
	if al.Edits.Names[into] == "" && al.Edits.Names[from] != "" {
		al.Edits.Names[into] = al.Edits.Names[from]
	}
	delete(al.Edits.Names, from)
	al.Regroup()
	return nil
}

// Move pins entry id to group to: a group id, Noise, or "new" (a group of its own).
func (al *Album) Move(id, to string) (string, error) {
	if _, ok := al.groupOf[id]; !ok {
		return "", fmt.Errorf("no entry %s", id)
	}
	if to == "new" {
		to = "n-" + id // not "g-"+id: that may be the group it is in
	} else if al.groups[to] == nil {
		return "", fmt.Errorf("no group %s", to)
	}
	al.Edits.Pins[id] = to
	al.Regroup()
	return to, nil
}

// Forget deletes group id and its entries (a person's voice gone from the household's memory).
// It returns how many entries went.
func (al *Album) Forget(id string) int {
	keep := al.Entries[:0]
	n := 0
	for _, e := range al.Entries {
		if al.groupOf[e.ID] == id {
			delete(al.Edits.Pins, e.ID)
			n++
			continue
		}
		keep = append(keep, e)
	}
	al.Entries = keep
	delete(al.Edits.Names, id)
	for eid, gid := range al.Edits.Pins {
		if gid == id {
			delete(al.Edits.Pins, eid)
		}
	}
	al.Regroup()
	return n
}

// Trim keeps the newest max entries, dropping the oldest unpinned ones first.
func (al *Album) Trim(maxEntries int) bool {
	over := len(al.Entries) - maxEntries
	if over <= 0 {
		return false
	}
	keep := al.Entries[:0]
	for _, e := range al.Entries {
		if _, pinned := al.Edits.Pins[e.ID]; over > 0 && !pinned {
			over--
			continue
		}
		keep = append(keep, e)
	}
	al.Entries = keep
	for over > 0 && len(al.Entries) > 0 { // all pinned: the oldest go anyway
		delete(al.Edits.Pins, al.Entries[0].ID)
		al.Entries = al.Entries[1:]
		over--
	}
	al.Regroup()
	return true
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
