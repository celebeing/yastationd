package voiceid

import (
	"fmt"
	"math/rand"
	"testing"
)

// voice makes n voiceprints around a random base: cosine to the base ≈ 0.8.
func voice(r *rand.Rand, n int, prefix string, female float64, at *int64) []*Entry {
	base := make([]float32, 64)
	for i := range base {
		base[i] = float32(r.NormFloat64())
	}
	var out []*Entry
	for k := 0; k < n; k++ {
		e := make([]float32, len(base))
		for i := range e {
			e[i] = base[i] + 0.6*float32(r.NormFloat64())
		}
		*at++
		out = append(out, &Entry{ID: fmt.Sprintf("%s%d", prefix, k), At: *at, Speaker: "s1", Secs: 2,
			Transcript: "включи свет", Female: female, Emb: e})
	}
	return out
}

func TestAlbumGroupsAndEdits(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	var at int64
	al := NewAlbum()
	a, b := voice(r, 20, "a", 0.1, &at), voice(r, 6, "b", 0.9, &at)
	for i := 0; i < 26; i++ { // interleaved, as a household talks
		if i < 20 {
			al.Add(a[i])
		}
		if i < 6 {
			al.Add(b[i])
		}
	}
	al.Add(&Entry{ID: "n1", At: 100, Speaker: "s2", Secs: 3, Emb: a[0].Emb}) // no transcript: noise
	al.Add(&Entry{ID: "n2", At: 101, Speaker: "s2", Secs: 0.4})              // no speech
	gs := al.Groups()
	if len(gs) != 3 || gs[0].Count != 20 || gs[1].Count != 6 || !gs[2].Noise || gs[2].Count != 2 {
		for _, g := range gs {
			t.Logf("%s n=%d noise=%v gender=%s", g.ID, g.Count, g.Noise, g.Gender)
		}
		t.Fatalf("want groups of 20, 6 and noise 2")
	}
	if gs[0].Gender != "male" || gs[1].Gender != "female" {
		t.Errorf("genders %s %s", gs[0].Gender, gs[1].Gender)
	}
	if len(al.People()) != 0 {
		t.Fatal("nobody is named yet: no people")
	}

	ga, gb := gs[0].ID, gs[1].ID
	if err := al.Rename(ga, "Петя"); err != nil {
		t.Fatal(err)
	}
	if len(al.People()) != 0 {
		t.Fatal("a name alone made a person of the household")
	}
	al.SetHousehold(ga, true)
	p := al.People()
	if len(p) != 1 || p[0].Name != "Петя" {
		t.Fatalf("people %+v", p)
	}
	// a new phrase of Петя joins the named group; the verdict takes it for him
	more := voice(r, 1, "x", 0.1, &at)
	more[0].Emb = a[3].Emb
	al.Add(more[0])
	if al.GroupOf("x0") != ga {
		t.Errorf("a new phrase went to %s, not %s", al.GroupOf("x0"), ga)
	}
	if v := Match(a[5].Emb, al.People(), 0.66, 0.02); v.State != Known || v.Name != "Петя" {
		t.Errorf("verdict %+v", v)
	}
	if v := Match(b[2].Emb, al.People(), 0.66, 0.02); v.State != Guest {
		t.Errorf("Аня should be a guest before she is named: %+v", v)
	}

	// a misgrouped entry moved by hand stays where it was put, through a regroup
	if _, err := al.Move("a7", gb); err != nil {
		t.Fatal(err)
	}
	al.Regroup()
	if al.GroupOf("a7") != gb {
		t.Error("a moved entry went back")
	}
	if g, _ := al.Move("a8", Noise); g != Noise || al.GroupOf("a8") != Noise {
		t.Error("move to noise")
	}

	// merge the names: Аня's group named, then merged into another of hers
	if _, err := al.Move("b0", "new"); err != nil {
		t.Fatal(err)
	}
	nb := al.GroupOf("b0")
	al.Rename(gb, "Аня")
	al.SetHousehold(gb, true)
	if err := al.Merge(nb, gb); err != nil {
		t.Fatal(err)
	}
	if al.GroupOf("b0") != gb {
		t.Error("merge did not move b0")
	}
	if len(al.People()) != 2 {
		t.Errorf("people %d", len(al.People()))
	}

	// forget Аня: her entries go, and so does she
	if n := al.Forget(gb); n != 7 { // b0..b5 + a7
		t.Errorf("forgot %d entries", n)
	}
	if len(al.People()) != 1 {
		t.Error("Аня still known")
	}
}

func TestAlbumTrimKeepsPinned(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	var at int64
	al := NewAlbum()
	for _, e := range voice(r, 10, "a", 0.1, &at) {
		al.Add(e)
	}
	al.Rename(al.GroupOf("a0"), "Петя") // pins a0..a9
	for _, e := range voice(r, 10, "c", 0.5, &at) {
		al.Add(e)
	}
	al.Trim(12)
	if len(al.Entries) != 12 {
		t.Fatalf("%d entries", len(al.Entries))
	}
	if al.GroupOf("a0") == "" {
		t.Error("a pinned entry was dropped before unpinned ones")
	}
}

// A voice whose first two phrases are atypical (far from each other) starts two groups entry by
// entry; the merge pass joins them once the rest show they are one voice (seen on a real
// household). A group the owner split off by hand stays apart.
func TestAlbumJoinsASplitVoice(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	var at int64
	al := NewAlbum()
	for _, e := range voice(r, 10, "a", 0.1, &at) {
		al.Add(e)
	}
	b := voice(r, 8, "b", 0.9, &at)
	odd := make([]float32, len(b[0].Emb))
	for i := range odd {
		odd[i] = float32(r.NormFloat64()) * 0.5
	}
	for i := range b[0].Emb { // b0 and b1 pulled apart in opposite directions
		b[0].Emb[i] += odd[i]
		b[1].Emb[i] -= odd[i]
	}
	if c := Cosine(b[0].Emb, b[1].Emb); c >= JoinThreshold {
		t.Fatalf("the setup wants b0, b1 far apart: %.2f", c)
	}
	for _, e := range b {
		al.Add(e)
	}
	if g := al.GroupOf("b0"); g != al.GroupOf("b1") || g != al.GroupOf("b7") || g == al.GroupOf("a0") {
		t.Fatalf("b split: %s %s %s (a: %s)", al.GroupOf("b0"), al.GroupOf("b1"), al.GroupOf("b7"), al.GroupOf("a0"))
	}
	// moved out by hand: stays out through regroups and new phrases
	al.Move("b3", "new")
	al.Add(voice(r, 1, "c", 0.9, &at)[0])
	al.Regroup()
	if al.GroupOf("b3") == al.GroupOf("b0") {
		t.Error("an entry the owner moved out was merged back")
	}
}
