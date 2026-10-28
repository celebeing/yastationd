package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/config"
	"github.com/celebeing/yastationd/yastationd/internal/display"
)

// newMusicApp is an app with a 21x12 pixel front that has the pause and resume patterns.
func newMusicApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	frame := strings.TrimSpace(strings.Repeat("ffffff ", 21*12)) + " 20\n"
	if err := os.MkdirAll(filepath.Join(dir, "media"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"Pause", "Resume"} {
		if err := os.WriteFile(filepath.Join(dir, "media", n+".led"), []byte(frame), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a := newExpressApp(t)
	a.matrix = nil
	dc := config.Default()
	a.conf.Store(&dc)
	a.front = display.PixelFront{W: 21, H: 12, Patterns: &display.PixelLibrary{Dirs: []string{dir}, W: 21, H: 12}}
	return a
}

func TestMusicTitleAndClips(t *testing.T) {
	a := newMusicApp(t)
	// A new track's metadata before playback starts: the title waits for it.
	a.onMusicMeta(`{"title":"Song","artist":"Band","duration_ms":200000,"progress_ms":0}`)
	if a.matrixStack.Has("music_title") {
		t.Fatal("title shown before playback started")
	}
	a.onMusicPlayback(true)
	if !a.matrixStack.Has("music_title") {
		t.Fatal("no title when the new track started")
	}
	a.matrixStack.Remove("music_title")

	// A track change: the group stops and plays again (Music Assistant restarts its stream):
	// neither animation.
	a.onMusicPlayback(false)
	a.onMusicPlayback(true)
	if a.matrixStack.Has("music") {
		t.Fatal("a stream restart showed pause or resume")
	}
	// A pause is the track's speed going to 0; a resume, back up. No title on resume.
	a.onMusicMeta(`{"title":"Song","artist":"Band","duration_ms":200000,"progress_ms":60000,"speed":0}`)
	if !a.matrixStack.Has("music") {
		t.Fatal("no pause animation")
	}
	a.matrixStack.Remove("music")
	a.onMusicMeta(`{"title":"Song","artist":"Band","duration_ms":200000,"progress_ms":60000,"speed":0}`)
	if a.matrixStack.Has("music") {
		t.Fatal("pause shown twice")
	}
	a.onMusicMeta(`{"title":"Song","artist":"Band","duration_ms":200000,"progress_ms":61000,"speed":1000}`)
	if !a.matrixStack.Has("music") || a.matrixStack.Has("music_title") {
		t.Fatalf("resume: animation %v, title %v", a.matrixStack.Has("music"), a.matrixStack.Has("music_title"))
	}
	// The next track while playing: its title.
	a.onMusicMeta(`{"title":"Other","artist":"Band","duration_ms":180000,"progress_ms":300}`)
	if !a.matrixStack.Has("music_title") {
		t.Fatal("no title for the next track")
	}
}

func TestMusicTitleNotForTrackAlreadyPlaying(t *testing.T) {
	a := newMusicApp(t)
	a.onMusicPlayback(true) // e.g. we restarted while music played
	a.onMusicMeta(`{"title":"Song","artist":"Band","duration_ms":200000,"progress_ms":95000}`)
	if a.matrixStack.Has("music_title") {
		t.Fatal("title for a track first seen in its middle")
	}
}

func TestMusicDisplayOff(t *testing.T) {
	a := newMusicApp(t)
	a.cfg().MusicTitle, a.cfg().MusicClips = false, false
	a.onMusicMeta(`{"title":"Song","duration_ms":200000,"progress_ms":0,"speed":1000}`)
	a.onMusicPlayback(true)
	a.onMusicMeta(`{"title":"Song","duration_ms":200000,"progress_ms":0,"speed":0}`)
	if a.matrixStack.Has("music_title") || a.matrixStack.Has("music") {
		t.Fatal("music on the display with music_title and music_clips off")
	}
}

func TestMusicBands(t *testing.T) {
	a := newMusicApp(t)
	now := time.Now()
	if a.musicBands(now) != nil {
		t.Fatal("bands without levels")
	}
	a.music.streaming.Store(true)
	a.onMusicLevels("00ff80")
	b := a.musicBands(time.Now())
	if len(b) != 3 || b[0] != 0 || b[1] != 1 || b[2] <= 0 || b[2] >= 1 {
		t.Fatalf("bands %v", b)
	}
	// A band that drops falls, not jumps.
	a.onMusicLevels("000000")
	b = a.musicBands(time.Now().Add(100 * time.Millisecond))
	if b[1] <= 0.5 || b[1] >= 1 {
		t.Fatalf("band fell to %v in 100 ms", b[1])
	}
	if a.musicBands(time.Now().Add(2*time.Second)) != nil {
		t.Fatal("stale levels still drawn")
	}
}

// A skip's stream restart looks like a short pause: the new title stays on top of it.
func TestMusicTitleOverPause(t *testing.T) {
	a := newMusicApp(t)
	a.onMusicPlayback(true)
	a.onMusicMeta(`{"title":"Song","artist":"Band","duration_ms":200000,"progress_ms":0,"speed":1000}`)
	a.onMusicMeta(`{"title":"Song","artist":"Band","duration_ms":200000,"progress_ms":1500,"speed":0}`)
	a.matrixStack.Frame(time.Now())
	if top := a.matrixStack.Top(); top.Name != "music_title" {
		t.Fatalf("on top: %q, want the title", top.Name)
	}
}
