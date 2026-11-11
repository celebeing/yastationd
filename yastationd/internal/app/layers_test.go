package app

import (
	"os"
	"path/filepath"
	"testing"
)

// A picture-only pack's call keeps the built-in ring tone and ring (Station 3, 2026-11-11: a
// drawn-faces pack set call_incoming to its "ringing" animation alone, and the call rang
// silently); a pack that empties a surface on purpose gets it empty.
func TestLayersPerSurface(t *testing.T) {
	a := pixelPackApp(t)
	os.WriteFile(filepath.Join(a.cfg().SysRoot, "cfw/sounds/calls_ringing_incoming.wav"), wavFile(make([]byte, 3200), 16000), 0o644)
	os.WriteFile(filepath.Join(a.cfg().SysRoot, "cfw/sounds/calls_connected.wav"), wavFile(make([]byte, 3200), 16000), 0o644)
	dir := filepath.Join(a.packDir("faces"), "anims", "pixels-21x12")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "ringing.led"), []byte(ledFrame()), 0o644)
	os.WriteFile(filepath.Join(a.packDir("faces"), "pack.json"), []byte(`{"id": "faces", "name": "Faces",
		"moments": {"pixels-21x12": {
			"call_incoming": ["ringing"],
			"call_connected": [{"steps": [{"anim": "ringing", "sound": "none", "ring": "none", "hold": true}]}]}}}`), 0o644)
	a.loadPacks()
	c := *a.cfg()
	c.Themes = []string{"faces"}
	a.conf.Store(&c)
	a.refreshPackAnims()

	layers := a.momentLayers("call_incoming")
	s, ok := a.firstAvailable(layers[0])
	if !ok || s.Ref != "ringing" {
		t.Fatalf("top: %+v %v", s, ok)
	}
	ring, sound := a.layered(layers, 0, s)
	if ring != "call_ringing" || len(sound) != 1 || sound[0].Sound != "calls_ringing_incoming|notification_sound" || !sound[0].Hold {
		t.Fatalf("the ring %q and the sound %+v from below", ring, sound)
	}
	if shown, _ := a.fire("call_incoming"); !shown || !a.ringStack.Has("intercom") || !a.matrixStack.Has("intercom") {
		t.Fatal("the picture or the ring did not show")
	}
	a.endHook("call_incoming")
	a.seqs.mu.Lock()
	left := a.seqs.stop[soundSlot("intercom")] != nil
	a.seqs.mu.Unlock()
	if left || a.ringStack.Has("intercom") {
		t.Fatal("the layered ring tone or ring outlived the moment")
	}

	layers = a.momentLayers("call_connected")
	s, _ = a.firstAvailable(layers[0])
	if ring, sound := a.layered(layers, 0, s); ring != "" || sound != nil {
		t.Fatalf("emptied on purpose, yet got the ring %q and the sound %+v", ring, sound)
	}
}
