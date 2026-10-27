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
	Ms      int    `json:"ms,omitempty"`      // how long; 0 = the animation's own length
	Hold    bool   `json:"hold,omitempty"`    // the last step stays (looping) until the slot moves on
}

func (st scenarioStep) anim() string {
	if st.Anim != "" {
		return st.Anim
	}
	return st.Matrix
}

const (
	stepDefault   = 2 * time.Second // a step with no length and no animation to take one from
	maxSequenceMs = 5 * 60 * 1000   // a step is cut at 5 min: a sequence is a show, not a mode
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

// stepAvailable: the step shows something on this speaker.
func (a *App) stepAvailable(st scenarioStep) bool {
	if st.Express != "" && a.refAvailable(st.Express) {
		return true
	}
	if st.Ring != "" && a.ring != nil && effect(st.Ring) != nil {
		return true
	}
	if n := st.anim(); n != "" && a.frontClips() {
		if _, ok := a.front.Anim(n); ok {
			return true
		}
	}
	return st.Text != "" && a.front != nil
}

// stepLength is how long a step lasts: its ms, else its animation's (or expression's) one pass.
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
	return stepDefault
}

// runSteps plays steps in slot. ttl > 0 ends the whole show after ttl; without it the show ends
// with its last step, or stays on a last step marked hold. ring, when set, is a ring effect kept
// under the whole show (a voice stage's ring under the owner's front-only sequence).
func (a *App) runSteps(slot string, steps []scenarioStep, ttl time.Duration, prio int, ring string, ringPrio int) {
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
			a.ringStack.Push(slot, ringPrio, ttl, r)
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
			a.showStep(slot, prio, st, 0, true, keepRing)
			return 0, true
		}
		d = a.stepLength(st)
		a.showStep(slot, prio, st, d, false, keepRing)
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
func (a *App) showStep(slot string, prio int, st scenarioStep, d time.Duration, hold, keepRing bool) {
	ring, front := keepRing, false
	if st.Express != "" {
		a.expressIn(slot, st.Express, d, prio, keepRing) // replaces or clears both surfaces itself
		ring, front = true, true
	}
	if st.Ring != "" {
		if r := effect(st.Ring); r != nil {
			a.ringStack.Push(slot, display.PrioScenario, d, r)
			ring = true
		}
	}
	if n := st.anim(); n != "" && a.frontClips() {
		if c, ok := a.front.Anim(n); ok {
			loop := c.Looping() || hold
			a.matrixStack.Push(slot, prio, d, c.Play(loop, !loop, a.brightness))
			front = true
		}
	}
	if st.Text != "" && a.front != nil {
		a.matrixStack.Push(slot, prio, d, a.front.Marquee(st.Text, a.marqueeStep(), a.brightness))
		front = true
	}
	if !ring {
		a.ringStack.Remove(slot)
	}
	if !front {
		a.matrixStack.Remove(slot)
	}
}
