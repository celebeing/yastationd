package display

import (
	"fmt"
	"strings"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/hw"
)

// The front display is driven through a Front: the catalogue of scenes and animations one kind
// of display can show (docs/display-expressions.md, "Front displays"). The app asks for a scene
// (the clock, a number, a countdown) or a semantic animation ("listening", "booting") and never
// for segments or pixels; each kind draws them its own way. A pixel screen that has no drawing
// of its own for a scene falls back to the 7-segment picture, rendered by Seg7ToPixels.

// FrontFrame is what a front-display layer draws: a 7-segment picture, or native pixels on a
// pixel screen. A pixel screen shows Seg through Seg7ToPixels when Px is nil (a scene without
// a pixel drawing of its own); a 7-segment display never gets Px.
type FrontFrame struct {
	Seg MatrixFrame
	Px  []hw.RGB // top row first, brightness applied; nil = no native picture
}

// SegLayer lifts a 7-segment render func onto the front stack.
func SegLayer(r RenderFunc[MatrixFrame]) RenderFunc[FrontFrame] {
	return func(now time.Time, below FrontFrame) (FrontFrame, bool) {
		f, done := r(now, below.Seg)
		return FrontFrame{Seg: f}, done
	}
}

// Anim is an animation native to one kind of front display (a seg7 clip, a pixel pattern).
type Anim interface {
	// Duration is one pass, an intro included.
	Duration() time.Duration
	// Looping: the animation is drawn to repeat (the stock file says "loop").
	Looping() bool
	// Play renders it from the first frame it is asked for. With loop it repeats (only the
	// looping part after an intro) until the layer is removed; otherwise the layer pops after
	// one pass, or with hold keeps the last frame until its TTL ends.
	Play(loop, hold bool, brightness func() int) RenderFunc[FrontFrame]
}

// Front is one kind of front display: the scenes the app shows and the animations it resolves.
type Front interface {
	// Kind describes the display for HA ("seg7", "pixels 21x12").
	Kind() string

	Clock(now time.Time, loc *time.Location, brightness int) FrontFrame
	// ClockDate is the clock with the date (the clock_date display mode); a display without room
	// for both shows the clock. HasClockDate tells whether it has the room (the mode is offered
	// only then).
	ClockDate(now time.Time, loc *time.Location, brightness int) FrontFrame
	HasClockDate() bool
	// Unsynced is the clock before the first time sync: no digits, a blinking colon.
	Unsynced(now time.Time, brightness int) FrontFrame
	Text(s string, brightness int) FrontFrame
	// Marquee shows s, scrolling when it does not fit.
	Marquee(s string, step time.Duration, brightness func() int) RenderFunc[FrontFrame]
	Number(n int, brightness int) FrontFrame
	// Volume is the volume indication (volume.go): stateful, set on every change.
	Volume(brightness func() int) VolumeScene
	Countdown(deadline time.Time, brightness func() int) RenderFunc[FrontFrame]
	// SetupMode is the Wi-Fi setup indication where the display has no "setup" animation.
	SetupMode(brightness int) FrontFrame

	// Anim resolves a semantic animation (an expression, "booting", "call_incoming", …) or a
	// plain animation name to this display's own animation.
	Anim(name string) (Anim, bool)
	// Anims lists the plain animation names available on this display.
	Anims() []string
}

// Seg7Front is a 4-digit 7-segment display; its animations are the stock clock clips.
type Seg7Front struct {
	Clips *ClipLibrary
}

func (Seg7Front) Kind() string { return "seg7" }

func (Seg7Front) Clock(now time.Time, loc *time.Location, brightness int) FrontFrame {
	return FrontFrame{Seg: Clock(now, loc, brightness)}
}

// HasClockDate: four digits have no room for the date as well.
func (f Seg7Front) HasClockDate() bool { return false }

// ClockDate: four digits have no room for the date as well.
func (f Seg7Front) ClockDate(now time.Time, loc *time.Location, brightness int) FrontFrame {
	return f.Clock(now, loc, brightness)
}

func (Seg7Front) Unsynced(now time.Time, brightness int) FrontFrame {
	return FrontFrame{Seg: MatrixFrame{Colon: now.Second()%2 == 0, Brightness: brightness}}
}

func (Seg7Front) Text(s string, brightness int) FrontFrame {
	return FrontFrame{Seg: Text(s, brightness)}
}

func (Seg7Front) Marquee(s string, step time.Duration, brightness func() int) RenderFunc[FrontFrame] {
	return SegLayer(Marquee(s, step, brightness))
}

func (Seg7Front) Number(n int, brightness int) FrontFrame {
	return FrontFrame{Seg: Number(n, brightness)}
}

func (Seg7Front) Countdown(deadline time.Time, brightness func() int) RenderFunc[FrontFrame] {
	return SegLayer(Countdown(deadline, brightness))
}

// SetupMode: two "eyes" and a raised dot, a face that is not the clock.
func (Seg7Front) SetupMode(brightness int) FrontFrame {
	f := Text("O  O", brightness)
	f.Degree = true
	return FrontFrame{Seg: f}
}

// seg7Anims maps semantic animations to clip names, the stock names across firmware versions
// first, then ours. An entry with no names means "none on this display": the name is not
// looked up as a clip.
var seg7Anims = map[string][]string{
	"listening":     {"alice_listening", "listening"},
	"thinking":      {"alice_thinking", "thinking"},
	"speaking":      {"alice_speaking", "speaking"},
	"waiting":       {"waiting"},
	"busy":          {"blink"},
	"error":         nil,
	"mute":          nil,
	"boot":          {"boot"},
	"booting":       {"sleep"},
	"ringing":       {"ringing"},
	"call_incoming": {"ringing"},
	"call_outgoing": {"ringing"},
	"setup":         nil,
	"pause":         {"player_pause", "pause"},
	"next":          {"player_next_track", "next_track"},
	"previous":      {"player_previous_track", "previous_track"},
	"eyes":          {"eyes", "configuring"},
	"blink":         {"blink"},
	"happy":         {"happy"},
	"sad":           {"sad"},
	"wink":          {"wink"},
	"sleep":         {"sleep"},
}

func (f Seg7Front) Anim(name string) (Anim, bool) {
	if f.Clips == nil {
		return nil, false
	}
	names, ok := seg7Anims[name]
	if !ok {
		names = []string{name}
	}
	if len(names) == 0 {
		return nil, false
	}
	c, ok := f.Clips.Load(names...)
	if !ok {
		return nil, false
	}
	return c, true
}

func (f Seg7Front) Anims() []string {
	if f.Clips == nil {
		return nil
	}
	return f.Clips.Names()
}

// PixelFront is a dot-matrix screen. Its animations are the firmware's own screen patterns; the
// clock is drawn natively (pixelclock.go). Scenes it has no drawing for yet (text, numbers,
// countdowns, the setup face) come from the embedded Seg7Front and are drawn by Seg7ToPixels:
// override a method here to give a scene a proper pixel drawing.
type PixelFront struct {
	Seg7Front
	W, H     int
	Patterns *PixelLibrary
	Gamma    float64 // the screen's gamma for our own drawings (the patterns carry their own)
}

func (f PixelFront) Kind() string { return fmt.Sprintf("pixels %dx%d", f.W, f.H) }

// pixelAnims maps semantic animations to the screen patterns of Station 3 firmware 3.21
// (ledpatterns/screen/, names relative to it). A candidate "a+b+c" plays a, b, then c; only the
// last part repeats when the animation loops. The oknix/ files are the voice stages in order:
// 01 the wake, 02 into listening, 03 listening, 04 into the answer, 05 thinking, 06 speaking.
var pixelAnims = map[string][]string{
	"listening":     {"oknix/01-activation+oknix/02-activation-to-listening+oknix/03-listening-loop"},
	"thinking":      {"oknix/04-listening-to-answer+oknix/05-thinking-loop"},
	"speaking":      {"oknix/06-answer-loop"},
	"waiting":       {"oknix/05-thinking-loop"},
	"busy":          nil,
	"error":         {"error"},
	"mute":          {"mute"},
	"boot":          {"boot"},
	"booting":       {"boot/FirstBoot_LoadLoop01"},
	"ringing":       {"alarm/alarm_loop"},
	"call_incoming": {"call_incoming"}, // its own intro, then a loop
	"call_outgoing": {"call_outgoing"},
	"setup":         {"configure"},
	"pause":         {"media/Pause"},
	"next":          {"media/Next"},
	"previous":      {"media/Previous"},
	"eyes":          {"scenario_emotion/Smile_Neutral_Output"},
	"blink":         {"scenario_emotion/Smile_Neutral_Output"},
	"happy":         {"emotion/joy"},
	"sad":           nil,
	"wink":          {"scenario_emotion/Smile_Wink_Output"},
	"sleep":         nil,
}

func (f PixelFront) Anim(name string) (Anim, bool) {
	if f.Patterns == nil {
		return nil, false
	}
	names, ok := pixelAnims[name]
	if !ok {
		names = []string{name}
	}
	if len(names) == 0 {
		return nil, false
	}
	p, ok := f.Patterns.Load(names...)
	if !ok {
		return nil, false
	}
	return p, true
}

func (f PixelFront) Anims() []string {
	if f.Patterns == nil {
		return nil
	}
	return f.Patterns.Names()
}

// ScreenPixels draws a front frame on a pixel screen: its own pixels, or the 7-segment picture via
// Seg7ToPixels. k (0..1) scales it (a layer fading in). nil when the screen is too small for the
// 7-segment fallback.
func ScreenPixels(f FrontFrame, w, h int, k float64) []hw.RGB {
	if f.Px == nil {
		f.Seg.Brightness = int(float64(f.Seg.Brightness) * k)
		return Seg7ToPixels(f.Seg, w, h, hw.RGB{R: 255, G: 255, B: 255})
	}
	if k >= 1 {
		return f.Px
	}
	out := make([]hw.RGB, len(f.Px))
	s := int(255 * k)
	for i, p := range f.Px {
		out[i] = p.Scale(s)
	}
	return out
}

// ScalePixels is px as seen under a backlight at bl (1..255), for previews; bl 0 (no backlight)
// returns px as is.
func ScalePixels(px []hw.RGB, bl int) []hw.RGB {
	if bl <= 0 || bl >= 255 {
		return px
	}
	out := make([]hw.RGB, len(px))
	for i, p := range px {
		out[i] = p.Scale(bl)
	}
	return out
}

// validAnimName accepts a pattern name relative to a library directory: no absolute path, no
// "..", no backslash, no hidden files.
func validAnimName(n string) bool {
	if n == "" || strings.HasPrefix(n, "/") || strings.Contains(n, "\\") {
		return false
	}
	for _, p := range strings.Split(n, "/") {
		if p == "" || p[0] == '.' {
			return false
		}
	}
	return true
}
