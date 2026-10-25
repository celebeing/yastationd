package app

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/display"
	"github.com/celebeing/yastationd/yastationd/internal/hw"
)

// commands maps each command object (the <obj> of <prefix>/<id>/<obj>/set; "display" is the
// display/cmd topic) to its handler. MQTT subscribes to all of them; the frame tap's control
// endpoint calls the same handlers, so the simulator and a unit take the same commands.
func (a *App) commands() map[string]func([]byte) {
	c := map[string]func([]byte){
		"ring":               a.cmdRing,
		"display_mode":       a.cmdDisplayMode,
		"display_message":    a.cmdDisplayMessage,
		"volume":             a.cmdVolume,
		"speaker":            a.cmdSpeaker,
		"show_text":          a.cmdShowText,
		"text_duration":      a.cmdDuration("text_duration", func(s *persistState) *int { return &s.TextSecs }),
		"ring_animation":     a.cmdRingAnimation,
		"follow_up":          a.cmdFollowUp,
		"animation_duration": a.cmdDuration("animation_duration", func(s *persistState) *int { return &s.AnimSecs }),
		"auto_brightness":    a.cmdAutoBrightness,
		"clock_brightness":   a.cmdClockBrightness,
		"display":            a.cmdDisplay,
		"expression":         a.cmdExpressionSelect,
		"express":            a.cmdExpress,
		"scenario":           a.cmdScenario,
	}
	if a.ambilight != nil {
		c["ambilight"] = a.cmdAmbilight
	}
	if a.frontClipsPossible() {
		c["display_faces"] = a.cmdFaces
	}
	return c
}

func (a *App) subscribe() {
	t := a.topics
	for obj, fn := range a.commands() {
		topic := t.Command(obj)
		if obj == "display" {
			topic = t.DisplayCmd()
		}
		a.mqtt.Subscribe(topic, fn)
	}
	a.intercomSubscribe()
}

// ring: HA JSON light schema {state, brightness, color:{r,g,b}, effect}.
func (a *App) cmdRing(payload []byte) {
	var m struct {
		State      string                   `json:"state"`
		Brightness *int                     `json:"brightness"`
		Color      *struct{ R, G, B uint8 } `json:"color"`
		Effect     *string                  `json:"effect"`
	}
	if err := json.Unmarshal(payload, &m); err != nil {
		a.log.Warn("bad ring command", "err", err)
		return
	}
	s := a.store.update(func(s *persistState) {
		if m.State != "" {
			s.RingOn = strings.EqualFold(m.State, "ON")
		}
		if m.Brightness != nil {
			s.RingBright = clamp(*m.Brightness, 0, 255)
		}
		if m.Color != nil {
			s.RingRGB = hw.RGB{R: m.Color.R, G: m.Color.G, B: m.Color.B}
		}
		if m.Effect != nil {
			if validRingEffect(*m.Effect) {
				s.RingEffect = *m.Effect
			} else {
				a.log.Warn("unknown ring effect", "effect", *m.Effect)
			}
		}
	})
	a.mqtt.Publish(a.topics.State("ring"), ringStateJSON(s))
}

type ambilightCommand struct {
	State      string                      `json:"state"`
	Brightness *int                        `json:"brightness"`
	Color      *struct{ R, G, B, W uint8 } `json:"color"`
}

func parseAmbilightCommand(payload []byte) (ambilightCommand, error) {
	var command ambilightCommand
	err := json.Unmarshal(payload, &command)
	return command, err
}

func (command ambilightCommand) apply(s *persistState) {
	if command.State != "" {
		s.AmbilightOn = strings.EqualFold(command.State, "ON")
	}
	if command.Brightness != nil {
		s.AmbilightBright = clamp(*command.Brightness, 0, 255)
	}
	if command.Color != nil {
		s.AmbilightRGBW = hw.RGBW{
			R: command.Color.R,
			G: command.Color.G,
			B: command.Color.B,
			W: command.Color.W,
		}
	}
}

// ambilight: HA JSON light schema {state, brightness, color:{r,g,b,w}}.
func (a *App) cmdAmbilight(payload []byte) {
	command, err := parseAmbilightCommand(payload)
	if err != nil {
		a.log.Warn("bad ambilight command", "err", err)
		return
	}
	s := a.store.update(command.apply)
	a.mqtt.Publish(a.topics.State("ambilight"), ambilightStateJSON(s))
}

func (a *App) cmdDisplayMode(payload []byte) {
	mode := strings.TrimSpace(string(payload))
	switch mode {
	case "clock", "off", "message":
	case videoMode: // a one-off playback over the persisted mode (video.go)
		if !a.startVideo() {
			a.publishDisplayMode() // puts HA's select back
			return
		}
		a.mqtt.Publish(a.topics.State("display_mode"), mode)
		return
	default:
		a.log.Warn("unknown display_mode", "mode", mode)
		return
	}
	a.stopVideo("display mode " + mode)
	a.store.update(func(s *persistState) { s.Mode = mode })
	a.mqtt.Publish(a.topics.State("display_mode"), mode)
}

func (a *App) cmdDisplayMessage(payload []byte) {
	msg := string(payload)
	a.stopVideo("display message")
	a.store.update(func(s *persistState) {
		s.Message = msg
		s.Mode = "message"
	})
	a.mqtt.Publish(a.topics.State("display_message"), msg)
	a.mqtt.Publish(a.topics.State("display_mode"), "message")
}

func (a *App) cmdVolume(payload []byte) {
	pct, err := strconv.Atoi(strings.TrimSpace(string(payload)))
	if err != nil {
		return
	}
	a.setVolumePercent(clamp(pct, 0, 100))
}

// speaker: a bare file path or URL to play. file:// and plain paths are played directly; a
// remote URL is rejected for now (no downloader wired yet) — announcements come from the bank.
func (a *App) cmdSpeaker(payload []byte) {
	s := strings.TrimSpace(string(payload))
	switch {
	case s == "" || s == "STOP":
		a.player.Stop()
	case strings.HasPrefix(s, "/"), strings.HasPrefix(s, "file://"):
		a.announce(strings.TrimPrefix(s, "file://"))
	default:
		a.log.Warn("speaker: only local paths are supported for now", "arg", s)
	}
}

// display/cmd one-shot overlay: {surface, op, args, ttl_ms, priority}.
func (a *App) cmdDisplay(payload []byte) {
	var c struct {
		Surface  string          `json:"surface"`
		Op       string          `json:"op"`
		Args     json.RawMessage `json:"args"`
		TTLms    int             `json:"ttl_ms"`
		Priority int             `json:"priority"`
	}
	if err := json.Unmarshal(payload, &c); err != nil {
		a.log.Warn("bad display/cmd", "err", err)
		return
	}
	ttl := time.Duration(c.TTLms) * time.Millisecond
	prio := c.Priority
	if prio == 0 {
		prio = display.PrioScenario
	}
	switch c.Surface {
	case "matrix":
		a.matrixOverlay(c.Op, c.Args, prio, ttl)
	case "ring":
		a.ringOverlay(c.Op, c.Args, prio, ttl)
	default:
		a.log.Warn("display/cmd: unknown surface", "surface", c.Surface)
	}
}

func (a *App) matrixOverlay(op string, args json.RawMessage, prio int, ttl time.Duration) {
	name := "ov_matrix"
	if a.front == nil {
		a.log.Info("display/cmd: no front display", "op", op)
		return
	}
	switch op {
	case "text", "message":
		var v struct {
			Text string `json:"text"`
		}
		json.Unmarshal(args, &v)
		a.matrixStack.Push(name, prio, ttl, a.front.Marquee(v.Text, marqueeStep, a.brightness))
	case "number":
		var v struct {
			N int `json:"n"`
		}
		json.Unmarshal(args, &v)
		a.matrixStack.Push(name, prio, ttl, func(time.Time, display.FrontFrame) (display.FrontFrame, bool) {
			return a.front.Number(v.N, a.brightness()), false
		})
	case "countdown":
		var v struct {
			Seconds  int    `json:"seconds"`
			Deadline string `json:"deadline"`
		}
		json.Unmarshal(args, &v)
		deadline := time.Now().Add(time.Duration(v.Seconds) * time.Second)
		if v.Deadline != "" {
			if dl, err := time.Parse(time.RFC3339, v.Deadline); err == nil {
				deadline = dl
			}
		}
		a.matrixStack.Push("countdown", prio, 0, a.front.Countdown(deadline, a.brightness))
	case "clear":
		a.matrixStack.Remove(name)
		a.matrixStack.Remove("countdown")
	case "clip", "frames":
		// "clip" names an animation of this display (or a semantic one: "listening"); "frames"
		// are hand-made seg7 frames, which a pixel screen draws through its seg7 fallback.
		var v struct {
			Name   string             `json:"name"`
			Loop   *bool              `json:"loop"`
			Frames []display.RawFrame `json:"frames"`
		}
		if err := json.Unmarshal(args, &v); err != nil {
			a.log.Warn("display/cmd: bad clip args", "err", err)
			return
		}
		var c display.Anim
		if op == "clip" {
			var ok bool
			if c, ok = a.front.Anim(v.Name); !ok {
				a.log.Info("display/cmd: no such clip", "name", v.Name)
				return
			}
		} else {
			raw, err := display.ClipFromRaw(v.Frames)
			if err != nil {
				a.log.Warn("display/cmd: bad frames", "err", err)
				return
			}
			c = raw
		}
		loop := c.Looping()
		if v.Loop != nil {
			loop = *v.Loop
		}
		a.matrixStack.Push(name, prio, ttl, c.Play(loop, false, a.brightness))
	default:
		a.log.Warn("matrix op unknown", "op", op)
	}
}

func (a *App) ringOverlay(op string, args json.RawMessage, prio int, ttl time.Duration) {
	name := "ov_ring"
	switch op {
	case "effect":
		// {name, color?: [r,g,b] | {r,g,b}, brightness?}: any ring effect (the light's effect_list)
		// as a timed overlay. Colour and brightness default to the ring light's own. One overlay
		// slot: a new animation replaces the running one.
		var v struct {
			Name       string          `json:"name"`
			Color      json.RawMessage `json:"color"`
			Brightness *int            `json:"brightness"`
		}
		json.Unmarshal(args, &v)
		var color *hw.RGB
		if rgb, ok := parseRGB(v.Color); ok {
			color = &rgb
		}
		a.playRingEffect(v.Name, color, v.Brightness, prio, ttl)
	case "solid":
		var v struct {
			R uint8 `json:"r"`
			G uint8 `json:"g"`
			B uint8 `json:"b"`
		}
		if json.Unmarshal(args, &v) == nil {
			c := hw.RGB{R: v.R, G: v.G, B: v.B}
			a.ringStack.Push(name, prio, ttl, func(_ time.Time, below display.RingFrame) (display.RingFrame, bool) {
				return display.Solid(len(below), c), false
			})
		}
	case "clear":
		a.ringStack.Remove(name)
	default:
		a.log.Warn("ring op unknown", "op", op)
	}
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// parseRGB accepts [r,g,b] (HA's color_rgb selector) or {"r":..,"g":..,"b":..}.
func parseRGB(raw json.RawMessage) (hw.RGB, bool) {
	var arr []int
	if json.Unmarshal(raw, &arr) == nil && len(arr) == 3 {
		return hw.RGB{R: uint8(clamp(arr[0], 0, 255)), G: uint8(clamp(arr[1], 0, 255)), B: uint8(clamp(arr[2], 0, 255))}, true
	}
	var obj struct{ R, G, B *int }
	if json.Unmarshal(raw, &obj) == nil && obj.R != nil && obj.G != nil && obj.B != nil {
		return hw.RGB{R: uint8(clamp(*obj.R, 0, 255)), G: uint8(clamp(*obj.G, 0, 255)), B: uint8(clamp(*obj.B, 0, 255))}, true
	}
	return hw.RGB{}, false
}

// playRingEffect runs a ring effect as the timed overlay (slot "ov_ring"). It lights the ring
// even when the HA light is off and doesn't touch the light's state: when the TTL ends the ring
// falls back to whatever the daemon shows underneath. Colour/brightness default to the light's.
func (a *App) playRingEffect(name string, color *hw.RGB, brightness *int, prio int, ttl time.Duration) bool {
	s := a.store.get()
	c, bright := s.RingRGB, s.RingBright
	if color != nil {
		c = *color
	}
	if brightness != nil {
		bright = clamp(*brightness, 0, 255)
	}
	if bright == 0 {
		bright = 255
	}
	r := baseEffect(name, c.Scale(bright), bright)
	if name == "solid" {
		sc := c.Scale(bright)
		r = func(_ time.Time, below display.RingFrame) (display.RingFrame, bool) {
			return display.Solid(len(below), sc), false
		}
	}
	if r == nil {
		a.log.Warn("ring effect unknown", "name", name)
		return false
	}
	if sp, ok := effectPrio[name]; ok && sp > prio {
		prio = sp
	}
	a.ringStack.Push("ov_ring", prio, ttl, r)
	return true
}

// show_text (notify): the message on the matrix for TextSecs, scrolling if long.
func (a *App) cmdShowText(payload []byte) {
	if a.front == nil {
		return
	}
	secs := a.store.get().TextSecs
	a.matrixStack.Push("ov_matrix", display.PrioScenario, time.Duration(secs)*time.Second,
		a.front.Marquee(string(payload), marqueeStep, a.brightness))
}

// ring_animation (select): play the chosen effect for AnimSecs, then report "off" again.
func (a *App) cmdRingAnimation(payload []byte) {
	name := strings.TrimSpace(string(payload))
	topic := a.topics.State("ring_animation")
	if name == animOff {
		a.ringStack.Remove("ov_ring")
		a.mqtt.Publish(topic, animOff)
		return
	}
	secs := a.store.get().AnimSecs
	if !a.playRingEffect(name, nil, nil, display.PrioScenario, time.Duration(secs)*time.Second) {
		return
	}
	a.mqtt.Publish(topic, name)
	a.animGen.Add(1)
	gen := a.animGen.Load()
	time.AfterFunc(time.Duration(secs)*time.Second, func() {
		if a.animGen.Load() == gen { // not superseded by a newer selection
			a.mqtt.Publish(topic, animOff)
		}
	})
}

// cmdDuration handles a persisted 1..600 s duration number.
func (a *App) cmdDuration(obj string, field func(*persistState) *int) func([]byte) {
	return func(payload []byte) {
		f, err := strconv.ParseFloat(strings.TrimSpace(string(payload)), 64)
		if err != nil {
			return
		}
		v := clamp(int(f), 1, 600)
		a.store.update(func(s *persistState) { *field(s) = v })
		a.mqtt.Publish(a.topics.State(obj), strconv.Itoa(v))
	}
}

func (a *App) cmdFollowUp(payload []byte) {
	mode := strings.TrimSpace(string(payload))
	for _, m := range followModes {
		if m == mode {
			a.store.update(func(s *persistState) { s.FollowUp = mode })
			a.mqtt.Publish(a.topics.State("follow_up"), mode)
			return
		}
	}
	a.log.Warn("unknown follow_up mode", "mode", mode)
}
