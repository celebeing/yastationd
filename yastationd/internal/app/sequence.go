package app

import (
	"sync"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/display"
)

// Sequences: steps played in one slot, one after another (a scenario, a moment's own sequence).
// Each slot runs at most one; showing anything else in the slot (express, unexpress, a new
// sequence) stops it. A step's fields play in parallel on their surfaces; the next step replaces
// them in place, so there is no blank between steps.

// scenarioStep is one step: an expression and/or explicit surface parts.
type scenarioStep struct {
	Express string `json:"express,omitempty"` // an expression (ring + front)
	Anim    string `json:"anim,omitempty"`    // an animation of the front display
	Matrix  string `json:"matrix,omitempty"`  // the same, the older name
	Ring    string `json:"ring,omitempty"`    // a ring effect (effects.go)
	Text    string `json:"text,omitempty"`    // front-display text (scrolls when long)
	Color   []int  `json:"color,omitempty"`   // [r,g,b] of a coloured ring shape (pulse, comet, blink, flash, solid)
	Period  int    `json:"period,omitempty"`  // its period in ms (0 = the shape's own)
	Else    string `json:"else,omitempty"`    // the front when the anim is not on this display: blank, setup, alarm
	Sound   string `json:"sound,omitempty"`   // a sound name (sounds.go), played beside everything else
	Phrase  string `json:"phrase,omitempty"`  // a sound name played as speech: other playback stops
	Ms      int    `json:"ms,omitempty"`      // how long; 0 = the animation's own length
	Hold    bool   `json:"hold,omitempty"`    // the last step stays (looping, its sound too) until the slot moves on
}

func (st scenarioStep) anim() string {
	if st.Anim != "" {
		return st.Anim
	}
	return st.Matrix
}

const (
	soundLoopGap  = 1500 * time.Millisecond // the pause between repeats of a held step's sound (a ring tone)
	stepDefault   = 2 * time.Second         // a step with no length and no animation to take one from
	maxSequenceMs = 5 * 60 * 1000           // a step is cut at 5 min: a sequence is a show, not a mode
)

type sequences struct {
	mu   sync.Mutex
	stop map[string]chan struct{}
}

// cancelSteps stops the sequence running in slot, if any (its layers stay for the caller).
func (a *App) cancelSteps(slot string) {
	a.seqs.mu.Lock()
	if c := a.seqs.stop[slot]; c != nil {
		close(c)
		delete(a.seqs.stop, slot)
	}
	a.seqs.mu.Unlock()
}

// stepsHaveRing: some step drives the ring (a ring effect, or an expression with a ring part).
func stepsHaveRing(steps []scenarioStep) bool {
	for _, st := range steps {
		if st.Ring != "" || expressions[st.Express].ring != "" {
			return true
		}
	}
	return false
}

// stepAvailable: the step shows or plays something on this speaker.
func (a *App) stepAvailable(st scenarioStep) bool {
	if st.Sound != "" && a.soundPath(st.Sound) != "" || st.Phrase != "" && a.soundPath(st.Phrase) != "" {
		return true
	}
	if st.Express != "" && a.refAvailable(st.Express) {
		return true
	}
	if st.Ring != "" && a.ring != nil && stepEffect(st.Ring, st.Color, st.Period) != nil {
		return true
	}
	if n := st.anim(); n != "" && a.frontClips() {
		if _, ok := a.front.Anim(n); ok {
			return true
		}
	}
	return st.Text != "" && a.front != nil
}

// stepLength is how long a step lasts: its ms, else its animation's (or expression's) one pass,
// else its sound's length.
func (a *App) stepLength(st scenarioStep) time.Duration {
	if st.Ms > 0 {
		return time.Duration(min(st.Ms, maxSequenceMs)) * time.Millisecond
	}
	for _, n := range []string{st.anim(), st.Express} {
		if n == "" || !a.frontClips() {
			continue
		}
		if c, ok := a.front.Anim(n); ok && c.Duration() > 0 {
			return c.Duration()
		}
	}
	if d := max(a.soundLen(st.Sound), a.soundLen(st.Phrase)); d > 0 {
		return d
	}
	return stepDefault
}

// runSteps plays steps in slot. ttl > 0 ends the whole show after ttl; without it the show ends
// with its last step, or stays on a last step marked hold. The steps' front parts show at prio,
// their ring parts at stepRing. ring, when set, is a ring effect kept under the whole show (a voice stage's ring under the owner's front-only sequence).
func (a *App) runSteps(slot string, steps []scenarioStep, ttl time.Duration, prio, stepRing int, ring string, ringPrio int) {
	a.cancelSteps(slot)
	stop := make(chan struct{})
	a.seqs.mu.Lock()
	if a.seqs.stop == nil {
		a.seqs.stop = map[string]chan struct{}{}
	}
	a.seqs.stop[slot] = stop
	a.seqs.mu.Unlock()
	keepRing := ring != ""
	if keepRing {
		if r := effect(ring); r != nil {
			a.pushRing(slot, ringPrio, ttl, r)
		}
	}
	mine := func() bool { // the slot still runs this show
		a.seqs.mu.Lock()
		defer a.seqs.mu.Unlock()
		return a.seqs.stop[slot] == stop
	}
	end := func() {
		a.seqs.mu.Lock()
		if a.seqs.stop[slot] == stop {
			delete(a.seqs.stop, slot)
			a.ringStack.Remove(slot)
			a.matrixStack.Remove(slot)
		}
		a.seqs.mu.Unlock()
	}
	var deadline <-chan time.Time
	if ttl > 0 {
		deadline = time.After(ttl)
	}
	// show starts step i (the first one before returning, so the caller's slot is never empty)
	// and reports how long it lasts; held = it stays until the slot moves on.
	show := func(i int) (d time.Duration, held bool) {
		st := steps[i]
		if i == len(steps)-1 && st.Hold {
			a.showStep(slot, prio, stepRing, st, 0, true, keepRing)
			a.loopSound(slot, st, stop, mine)
			return 0, true
		}
		d = a.stepLength(st)
		a.showStep(slot, prio, stepRing, st, d, false, keepRing)
		return d, false
	}
	if len(steps) == 0 {
		end()
		return
	}
	d, held := show(0)
	go func() {
		for i := 1; ; i++ {
			var next <-chan time.Time
			if !held {
				next = time.After(d)
			}
			select {
			case <-stop:
				return
			case <-deadline:
				end()
				return
			case <-next:
			}
			if i == len(steps) {
				end()
				return
			}
			if !mine() {
				return
			}
			d, held = show(i)
		}
	}()
}

// showStep puts one step on the surfaces in slot, replacing the previous step's layers in place;
// a surface the step does not name is cleared (the ring not when keepRing). d is the step's
// length (0 = until the slot moves on); hold loops its animation.
func (a *App) showStep(slot string, prio, ringPrio int, st scenarioStep, d time.Duration, hold, keepRing bool) {
	ring, front := keepRing, false
	if st.Express != "" {
		a.expressIn(slot, st.Express, d, prio, keepRing) // replaces or clears both surfaces itself
		ring, front = true, true
	}
	if st.Ring != "" {
		if r := stepEffect(st.Ring, st.Color, st.Period); r != nil {
			a.pushRing(slot, ringPrio, d, r)
			ring = true
		}
	}
	if n := st.anim(); n != "" && a.frontClips() {
		if c, ok := a.front.Anim(n); ok {
			loop := c.Looping() || hold
			a.pushFront(slot, prio, d, c.Play(loop, !loop, a.brightness))
			front = true
		}
	}
	if st.Text != "" && a.front != nil {
		a.pushFront(slot, prio, d, a.front.Marquee(st.Text, a.marqueeStep(), a.brightness))
		front = true
	}
	if !front && st.Else != "" {
		if r := a.elseFront(st.Else); r != nil {
			a.pushFront(slot, prio, d, r)
			front = true
		}
	}
	if st.Sound != "" {
		a.playSound(slot, st.Sound, false)
	}
	if st.Phrase != "" {
		a.playSound(slot, st.Phrase, true)
	}
	if !ring {
		a.ringStack.Remove(slot)
	}
	if !front {
		a.matrixStack.Remove(slot)
	}
}

// elseFront is a step's front when its animation is not on this display: "blank" (a dark front:
// the time is unknown while booting), "setup" (the display's setup-mode scene), "alarm" (a
// blinking 00:00). nil for an unknown one or no front display.
func (a *App) elseFront(name string) display.RenderFunc[display.FrontFrame] {
	if a.front == nil {
		return nil
	}
	switch name {
	case "blank":
		return func(time.Time, display.FrontFrame) (display.FrontFrame, bool) { return display.FrontFrame{}, false }
	case "setup":
		return func(time.Time, display.FrontFrame) (display.FrontFrame, bool) {
			return a.front.SetupMode(a.brightness()), false
		}
	case "alarm":
		return func(now time.Time, _ display.FrontFrame) (display.FrontFrame, bool) {
			if now.UnixMilli()/400%2 == 0 {
				return a.front.Text("00:00", a.brightness()), false
			}
			return display.FrontFrame{}, false
		}
	}
	return nil
}

// pushRing and pushFront put a moment's layer on a surface; in a slot that yields (setYield) its
// ring turns see-through while the slot yields (the front never does).
func (a *App) pushRing(slot string, prio int, ttl time.Duration, r display.RenderFunc[display.RingFrame]) {
	a.ringStack.Push(slot, prio, ttl, yielding(a.yieldOf(slot), r))
}

func (a *App) pushFront(slot string, prio int, ttl time.Duration, r display.RenderFunc[display.FrontFrame]) {
	a.matrixStack.Push(slot, prio, ttl, r)
}

func yielding[F any](yield func() bool, r display.RenderFunc[F]) display.RenderFunc[F] {
	if yield == nil {
		return r
	}
	return func(now time.Time, below F) (F, bool) {
		if yield() {
			return below, false
		}
		return r(now, below)
	}
}

// setYield makes slot's ring see-through while yield is true (nil: never): a call's ring under
// the voice stages (the listening window after «Алиса», an Assist session in the call).
func (a *App) setYield(slot string, yield func() bool) {
	if yield == nil {
		a.yields.Delete(slot)
		return
	}
	a.yields.Store(slot, yield)
}

func (a *App) yieldOf(slot string) func() bool {
	if y, ok := a.yields.Load(slot); ok {
		return y.(func() bool)
	}
	return nil
}

// loopSound repeats a held step's sound (soundLoopGap apart) while the slot runs this show (stop
// open and mine): a ring tone. The step itself played it once already.
func (a *App) loopSound(slot string, st scenarioStep, stop <-chan struct{}, mine func() bool) {
	name, phrase := st.Sound, false
	if name == "" {
		name, phrase = st.Phrase, true
	}
	d := a.soundLen(name)
	if d == 0 {
		return
	}
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(d + soundLoopGap):
			}
			if !mine() {
				return
			}
			a.playSound(slot, name, phrase)
		}
	}()
}

// endShow ends what slot shows and plays: the sequence, its layers and its sound.
func (a *App) endShow(slot string) {
	a.unexpress(slot)
	if a.player != nil {
		a.player.StopBlipOn(slot)
	}
}

// soundEnd is how long after its start a sequence's last sound ends (0: it plays none here): what
// a caller waits for before its own audio (the announce chime before an announcement).
func (a *App) soundEnd(steps []scenarioStep) time.Duration {
	var at, end time.Duration
	for _, st := range steps {
		if d := max(a.soundLen(st.Sound), a.soundLen(st.Phrase)); d > 0 {
			end = max(end, at+d)
		}
		at += a.stepLength(st)
	}
	return end
}
