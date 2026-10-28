package app

import (
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/display"
	"github.com/celebeing/yastationd/yastationd/internal/hw"
)

// effect maps a name to a ring render func. Returns nil for an unknown name.
func effect(name string) display.RenderFunc[display.RingFrame] {
	switch name {
	case "listening":
		return display.Pulse(display.Cyan, 1500*time.Millisecond)
	case "thinking":
		return display.Comet(display.White, 900*time.Millisecond)
	case "speaking":
		return display.Pulse(display.Green, 700*time.Millisecond)
	case "error":
		return display.Blink(display.Red, 300*time.Millisecond)
	case "success":
		return display.Flash(display.Green, 800*time.Millisecond)
	case "boot":
		return display.Comet(display.Cyan, 1200*time.Millisecond)
	case "waiting": // a long wait for HA's reply: the thinking comet, slower and dimmer
		return display.Comet(display.White.Scale(110), 2400*time.Millisecond)
	case "busy": // heard the wake word, but a reply is still pending
		return display.Flash(display.White, 500*time.Millisecond)
	case "rainbow":
		return display.Rainbow(3*time.Second, 255)
	case "mute":
		return func(_ time.Time, below display.RingFrame) (display.RingFrame, bool) {
			return display.Solid(len(below), display.Amber), false
		}
	// The status moments' rings (docs/status-indication.md), named so a theme can pick them.
	case "booting":
		return display.Pulse(display.Blue, 2*time.Second)
	case "boot_failed":
		return display.Pulse(display.Red, 2*time.Second)
	case "degraded":
		return display.Pulse(display.Red.Scale(40), 3*time.Second)
	case "unavailable":
		return display.Pulse(display.Red, 700*time.Millisecond)
	case "calling":
		return display.Comet(display.Green, 1200*time.Millisecond)
	case "call_ringing":
		return display.Blink(display.Green, time.Second)
	case "in_call": // blue is no other state's colour
		return display.Pulse(display.Blue, 2500*time.Millisecond)
	case "call_end":
		return display.Flash(display.White, 500*time.Millisecond)
	case "alarm":
		return display.Blink(display.Amber, 400*time.Millisecond)
	case "setup":
		return display.Comet(display.Yellow, 1200*time.Millisecond)
	default:
		return nil
	}
}

// statusEffects are the named rings of the status moments, beside ringEffects in the moments
// editor (not in HA's effect list).
var statusEffects = []string{"mute", "booting", "boot_failed", "degraded", "unavailable", "calling",
	"call_ringing", "in_call", "call_end", "alarm", "setup", "waiting", "busy", "success"}

// colourEffects take a step's colour and period: the shapes of the presets, in any colour.
var colourEffects = map[string]time.Duration{
	"pulse": 1500 * time.Millisecond,
	"comet": 1200 * time.Millisecond,
	"blink": 500 * time.Millisecond,
	"flash": 500 * time.Millisecond,
	"solid": 0,
}

// stepEffect is a step's ring: a shape in the step's colour (white when none) and period, or a
// named effect. nil for an unknown name.
func stepEffect(name string, rgb []int, periodMs int) display.RenderFunc[display.RingFrame] {
	def, ok := colourEffects[name]
	if !ok {
		return effect(name)
	}
	c := display.White
	if len(rgb) == 3 {
		c = hw.RGB{R: uint8(clamp(rgb[0], 0, 255)), G: uint8(clamp(rgb[1], 0, 255)), B: uint8(clamp(rgb[2], 0, 255))}
	}
	period := def
	if periodMs > 0 {
		period = time.Duration(periodMs) * time.Millisecond
	}
	switch name {
	case "pulse":
		return display.Pulse(c, period)
	case "comet":
		return display.Comet(c, period)
	case "blink":
		return display.Blink(c, period)
	case "flash":
		return display.Flash(c, period)
	}
	return func(_ time.Time, below display.RingFrame) (display.RingFrame, bool) {
		return display.Solid(len(below), c), false
	}
}

// effectPrio raises a few effects above the default scenario priority so they win as the
// design's priority order requires (error > voice-state > local-transient > scenario).
var effectPrio = map[string]int{
	"listening": display.PrioVoice,
	"thinking":  display.PrioVoice,
	"speaking":  display.PrioVoice,
	"error":     display.PrioError,
	"mute":      display.PrioLocal,
}

// ringEffects is the HA light effect_list. "none" is the plain solid colour; pulse/comet/blink
// animate the light's own colour and brightness; rainbow uses the brightness only; the rest are
// the named fixed-colour presets (also usable as display/cmd overlays).
var ringEffects = []string{"none", "pulse", "comet", "blink", "rainbow",
	"listening", "thinking", "speaking", "error", "boot"}

func validRingEffect(name string) bool {
	for _, e := range ringEffects {
		if e == name {
			return true
		}
	}
	return false
}

// baseEffect renders the ring's base layer for an HA-selected effect; c is already
// brightness-scaled. Returns nil for "none" (solid).
func baseEffect(name string, c hw.RGB, bright int) display.RenderFunc[display.RingFrame] {
	switch name {
	case "pulse":
		return display.Pulse(c, 1500*time.Millisecond)
	case "comet":
		return display.Comet(c, 1200*time.Millisecond)
	case "blink":
		return display.Blink(c, 500*time.Millisecond)
	case "rainbow":
		return display.Rainbow(3*time.Second, bright)
	case "none", "":
		return nil
	}
	if r := effect(name); r != nil {
		return scaleEffect(r, bright)
	}
	return nil
}

// scaleEffect applies the light brightness to a fixed-colour preset.
func scaleEffect(r display.RenderFunc[display.RingFrame], bright int) display.RenderFunc[display.RingFrame] {
	return func(now time.Time, below display.RingFrame) (display.RingFrame, bool) {
		f, done := r(now, below)
		for i := range f {
			f[i] = f[i].Scale(bright)
		}
		return f, done
	}
}

// animOff is the ring_animation select's idle option. Not "none": HA's MQTT select treats a
// "None" state payload (any case) as a reset to unknown.
const animOff = "off"
