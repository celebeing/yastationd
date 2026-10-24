package hw

import (
	"os"
	"path/filepath"
	"testing"
)

// FindDotMatrix must pick the fb whose name carries "LED screen" (orion's fb1 says
// "YNDX LED screen") and skip the others (ring fb2, ambilight fb0).
func TestFindDotMatrix(t *testing.T) {
	root := t.TempDir()
	for i, name := range map[int]string{0: "ambilight", 1: "YNDX LED screen", 2: "ring"} {
		p := filepath.Join(root, "sys/class/graphics", "fb"+string(rune('0'+i)))
		os.MkdirAll(p, 0o755)
		os.WriteFile(filepath.Join(p, "name"), []byte(name), 0o644)
	}
	if got := FindDotMatrix(root); got != root+"/dev/fb1" {
		t.Fatalf("FindDotMatrix = %q, want %s", got, root+"/dev/fb1")
	}
	skip := t.TempDir()
	os.MkdirAll(filepath.Join(skip, "sys/class/graphics/fb0"), 0o755)
	os.WriteFile(filepath.Join(skip, "sys/class/graphics/fb0/name"), []byte("ring"), 0o644)
	if got := FindDotMatrix(skip); got != "" {
		t.Fatalf("FindDotMatrix = %q, want \"\"", got)
	}
}

func TestDotMatrixEncodeStrideAndBGR(t *testing.T) {
	// Two 24bpp pixels per row, plus two padding bytes. order={2,1,0} means memory BGR.
	m := &DotMatrix{w: 2, h: 2, stride: 8, pb: 3, order: [3]int{2, 1, 0}, scratch: make([]byte, 16)}
	px := []RGB{{R: 1, G: 2, B: 3}, {}, {}, {R: 4, G: 5, B: 6}}
	got := m.encode(px)
	want := []byte{
		3, 2, 1, 0, 0, 0, 0, 0, // row 0 + two pad bytes
		0, 0, 0, 6, 5, 4, 0, 0, // row 1 + two pad bytes
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("byte %d = %d, want %d (%v)", i, got[i], want[i], got)
		}
	}
}

func TestDotMatrixCloseIsIdempotent(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "fb")
	if err != nil {
		t.Fatal(err)
	}
	m := &DotMatrix{f: f}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
}
