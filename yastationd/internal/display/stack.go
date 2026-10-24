// Package display implements the per-surface layer stack: the highest-priority live layer
// wins, layers expire on a TTL, and the surface falls back to the base (clock / HA light).
package display

import (
	"sort"
	"sync"
	"time"
)

// Priorities, high to low (hass-integration.md §2.6).
const (
	PrioBase     = 0
	PrioDegraded = 5 // subtle persistent indicator; sits just above the base
	PrioScenario = 20
	// Expressions on the front display (faces, clips): above HA overlays, below the local
	// reflexes (volume bubble, timer ring), which must stay readable. The ring's voice stages
	// keep PrioVoice.
	PrioExpression = 25
	PrioLocal      = 30
	PrioVoice      = 40
	PrioError      = 50
	// Wi-Fi setup mode: the speaker is not itself for as long as it lasts.
	PrioSetup = 60
)

// RenderFunc draws a frame at time now. `below` is what the layers underneath would show, so
// a layer can draw on top of it (e.g. one dot) instead of replacing it. done=true pops the layer.
type RenderFunc[F any] func(now time.Time, below F) (frame F, done bool)

type layer[F any] struct {
	name    string
	prio    int
	seq     int
	expires time.Time // zero = no TTL
	render  RenderFunc[F]
}

type Stack[F any] struct {
	mu     sync.Mutex
	base   func(now time.Time) F
	layers map[string]*layer[F]
	seq    int
	top    Top
}

// Top identifies the layer that drew the last frame on top: the base has Name "" and Prio
// PrioBase. Seq changes when a layer is replaced under the same name.
type Top struct {
	Name string
	Prio int
	Seq  int
}

// Top reports which layer drew the last frame on top.
func (s *Stack[F]) Top() Top {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.top
}

func NewStack[F any](base func(now time.Time) F) *Stack[F] {
	return &Stack[F]{base: base, layers: map[string]*layer[F]{}}
}

// Push adds or replaces the layer `name`. ttl <= 0 means it stays until removed or done.
func (s *Stack[F]) Push(name string, prio int, ttl time.Duration, r RenderFunc[F]) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	l := &layer[F]{name: name, prio: prio, seq: s.seq, render: r}
	if ttl > 0 {
		l.expires = time.Now().Add(ttl)
	}
	s.layers[name] = l
}

func (s *Stack[F]) Remove(name string) {
	s.mu.Lock()
	delete(s.layers, name)
	s.mu.Unlock()
}

func (s *Stack[F]) Has(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.layers[name]
	return ok
}

// Frame composes the stack bottom-up: base, then every live layer in priority order (ties:
// the newest on top). Expired and finished layers are dropped.
func (s *Stack[F]) Frame(now time.Time) F {
	s.mu.Lock()
	ls := make([]*layer[F], 0, len(s.layers))
	for name, l := range s.layers {
		if !l.expires.IsZero() && now.After(l.expires) {
			delete(s.layers, name)
			continue
		}
		ls = append(ls, l)
	}
	s.mu.Unlock()

	sort.Slice(ls, func(i, j int) bool {
		if ls[i].prio != ls[j].prio {
			return ls[i].prio < ls[j].prio
		}
		return ls[i].seq < ls[j].seq
	})
	f := s.base(now)
	top := Top{Prio: PrioBase}
	for _, l := range ls {
		nf, done := l.render(now, f)
		if done {
			s.mu.Lock()
			if s.layers[l.name] == l {
				delete(s.layers, l.name)
			}
			s.mu.Unlock()
			continue
		}
		f = nf
		top = Top{Name: l.name, Prio: l.prio, Seq: l.seq}
	}
	s.mu.Lock()
	s.top = top
	s.mu.Unlock()
	return f
}

// Fader turns a change of the top layer into a brightness ramp from 0: Up when a higher layer
// takes over (it must be readable at once, e.g. the volume number), Down when a lower one comes
// back (the clock after a face), so a switch never snaps.
type Fader struct {
	Up, Down time.Duration

	last  Top
	known bool
	start time.Time
	dur   time.Duration
}

// Level is the brightness factor 0..1 for a frame drawn at now by top.
func (f *Fader) Level(now time.Time, top Top) float64 {
	if !f.known {
		f.last, f.known = top, true
		return 1
	}
	if top != f.last {
		f.dur = f.Up
		if top.Prio < f.last.Prio {
			f.dur = f.Down
		}
		f.start, f.last = now, top
	}
	if f.dur <= 0 {
		return 1
	}
	el := now.Sub(f.start)
	if el >= f.dur {
		return 1
	}
	return float64(el) / float64(f.dur)
}
