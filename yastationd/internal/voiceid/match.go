package voiceid

import "math"

// Person is one of the household's voices as the speakers know it: a name and the centroid of
// their voiceprints (the store publishes them; docs/voice-id.md).
type Person struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Gender   string    `json:"gender,omitempty"`
	Centroid []float32 `json:"centroid"`
}

// Verdict states.
const (
	Known   = "known"   // one of the household
	Guest   = "guest"   // speech, but nobody enrolled is close enough
	Unknown = "unknown" // too little speech, or nobody enrolled yet: no verdict
)

// Verdict is who a voiceprint was taken for.
type Verdict struct {
	State      string  `json:"state"`
	ID         string  `json:"id,omitempty"`   // the person's, when known
	Name       string  `json:"name,omitempty"` // the person's, when known
	Score      float64 `json:"score"`          // cosine to the closest person
	Closest    string  `json:"closest,omitempty"`
	Second     float64 `json:"second"` // cosine to the runner-up
	SecondName string  `json:"second_name,omitempty"`
	Gender     string  `json:"gender"`             // male, female, unknown
	Female     float64 `json:"female"`             // the gender classifier's output (-1: none)
	Age        float64 `json:"age"`                // the age classifier's output (-1: none)
	Secs       float64 `json:"secs"`               // speech the voiceprint was taken on
	Model      string  `json:"model,omitempty"`    // the voiceprint model's tag
	Stage      string  `json:"stage,omitempty"`    // early (the wake word) or final (the whole request)
	Speaker    string  `json:"speaker,omitempty"`  // the speaker that heard it
	Embedding  []float32 `json:"-"`
}

// Gender bands of the firmware's classifier (P(female); its own threshold is 0.55, non-speech
// sits near 0.5).
const (
	femaleAbove = 0.65
	maleBelow   = 0.40
)

// GenderOf names the classifier's output.
func GenderOf(female float64) string {
	switch {
	case female < 0:
		return Unknown
	case female >= femaleAbove:
		return "female"
	case female <= maleBelow:
		return "male"
	}
	return Unknown
}

// Cosine similarity of two voiceprints (0 for an empty or mismatched pair).
func Cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var ab, aa, bb float64
	for i := range a {
		ab += float64(a[i]) * float64(b[i])
		aa += float64(a[i]) * float64(a[i])
		bb += float64(b[i]) * float64(b[i])
	}
	if aa == 0 || bb == 0 {
		return 0
	}
	return ab / math.Sqrt(aa*bb)
}

// Match takes emb for the closest person when its cosine reaches threshold and beats the
// runner-up by margin; otherwise a guest. With nobody enrolled the state is Unknown.
func Match(emb []float32, people []Person, threshold, margin float64) Verdict {
	v := Verdict{State: Unknown, Female: -1, Age: -1, Gender: Unknown, Embedding: emb}
	if len(emb) == 0 || len(people) == 0 {
		return v
	}
	best, second := -2.0, -2.0
	var bp, sp *Person
	for i := range people {
		p := &people[i]
		c := Cosine(emb, p.Centroid)
		switch {
		case c > best:
			second, sp = best, bp
			best, bp = c, p
		case c > second:
			second, sp = c, p
		}
	}
	v.Score, v.Closest = round3(best), bp.Name
	if sp != nil {
		v.Second, v.SecondName = round3(second), sp.Name
	}
	v.State = Guest
	if best >= threshold && (sp == nil || best-second >= margin) {
		v.State, v.ID, v.Name = Known, bp.ID, bp.Name
	}
	return v
}

func round3(x float64) float64 { return math.Round(x*1000) / 1000 }
