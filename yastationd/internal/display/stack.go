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
	PrioLocal    = 30
	PrioVoice    = 40
	PrioError    = 50
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
	}
	return f
}
