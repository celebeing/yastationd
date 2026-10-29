package voiceid

import (
	"bufio"
	"encoding/json"
	"math"
	"os"
	"testing"
)

// TestClassifiersAgainstTFLite checks the Go classifiers against TFLite's own outputs: run on the
// desk with YST_BIO=<a bio_lingware dir> and YST_BIO_REF=<JSON lines {"emb", "gender", "age"}>
// (the voice lab's output); skipped otherwise (the firmware's models are not in the repo).
func TestClassifiersAgainstTFLite(t *testing.T) {
	dir, ref := os.Getenv("YST_BIO"), os.Getenv("YST_BIO_REF")
	if dir == "" || ref == "" {
		t.Skip("YST_BIO / YST_BIO_REF not set")
	}
	g, err := LoadClassifier(dir + "/tflite-linear-gender-classifier/gender_classifier.tflite")
	if err != nil {
		t.Fatal(err)
	}
	a, err := LoadClassifier(dir + "/tflite-linear-age-classifier/age_classifier.tflite")
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(ref)
	if err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	n := 0
	for sc.Scan() {
		var r struct {
			Emb         []float32
			Gender, Age float64
		}
		if json.Unmarshal(sc.Bytes(), &r) != nil || len(r.Emb) == 0 {
			continue
		}
		gg, aa := g.Predict(r.Emb), a.Predict(r.Emb)
		if math.Abs(gg-r.Gender) > 0.02 || math.Abs(aa-r.Age) > 0.02 {
			t.Errorf("line %d: gender %.3f vs %.3f, age %.3f vs %.3f", n, gg, r.Gender, aa, r.Age)
		}
		n++
	}
	t.Logf("%d voiceprints compared", n)
}
