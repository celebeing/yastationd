package app

import (
	"encoding/json"
	"math"
	"sync"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/wake"
)

// Wake-word arbitration (docs/wake-arbitration.md): when several speakers hear one «Алиса», the
// loudest answers. Each posts a claim — its device id and the wake word's level in dB SPL (the
// boards' mic calibrations make levels comparable across models) — on the shared arbitration
// topic, and decides by itself on the claims it has: a loud claim (well above the room's floor)
// after a short wait, a quiet one after cfg.ArbitrationWait, or at once when a louder claim
// arrives. The loser stays silent; the winner starts the session with the speech buffered
// meanwhile, so nothing said after the wake word is lost. With no broker, or the feature off,
// every speaker answers as before.

const (
	arbWakeSpan   = 1200 * time.Millisecond // the wake word's level: the last 1.2 s before the event
	arbLoudest    = 8                       // … the energy average of its loudest 20 ms blocks
	arbQuickWait  = 200 * time.Millisecond  // a loud claim's wait
	arbLoudOver   = 25.0                    // dB over the room's floor that makes a claim loud
	arbRoundSpan  = 1500 * time.Millisecond // claims this close in arrival are one round
	arbTieDB      = 0.5                     // levels this close tie; the lower id wins
	arbKeepClaims = 5 * time.Second
)

type arbClaim struct {
	ID    string  `json:"id"`
	Level float64 `json:"level"` // dB SPL (dBFS + the mic calibration)
	At    int64   `json:"at"`    // the sender's clock, unix ms (for the logs only)
}

type seenClaim struct {
	arbClaim
	recv time.Time
}

type arbiter struct {
	mu     sync.Mutex
	claims []seenClaim // other speakers' recent claims
	open   *arbRound   // our claim waiting for its decision
}

type arbRound struct {
	mine  arbClaim
	at    time.Time
	timer *time.Timer
	done  bool
	held  *heldWake
}

func (a *App) arbitrationOn() bool {
	return a.cfg().Arbitration && a.mqtt != nil && a.mqtt.Connected() && a.room != nil
}

// onArbitrationClaim takes a claim from the topic (our own comes back too).
func (a *App) onArbitrationClaim(payload []byte) {
	var c arbClaim
	if json.Unmarshal(payload, &c) != nil || c.ID == "" || c.ID == a.cfg().DeviceID {
		return
	}
	now := time.Now()
	a.log.Info("arbitration: claim", "from", c.ID, "level", round1(c.Level))
	a.arb.mu.Lock()
	keep := a.arb.claims[:0]
	for _, s := range a.arb.claims {
		if now.Sub(s.recv) < arbKeepClaims {
			keep = append(keep, s)
		}
	}
	a.arb.claims = append(keep, seenClaim{c, now})
	r := a.arb.open
	a.arb.mu.Unlock()
	if r != nil && beats(c, r.mine) {
		a.decide(r) // a louder speaker heard it: no need to wait
	}
}

// arbitrateWake is the wake word's path when arbitration is on: claim, buffer the speech, decide.
func (a *App) arbitrateWake(ev wake.Event) {
	level, over, ok := a.room.peak(arbWakeSpan, arbLoudest)
	if !ok {
		a.startWake(ev) // no levels yet: answer as before
		return
	}
	mine := arbClaim{ID: a.cfg().DeviceID, Level: round1(level + a.splOffset()), At: time.Now().UnixMilli()}
	r := &arbRound{mine: mine, at: time.Now(), held: &heldWake{take: a.voiceTakeAt(&ev, srcWake)}}
	r.held.unsub = a.wakeAudio(&ev, r.held.add)
	if b, err := json.Marshal(mine); err == nil {
		a.publishEvent(a.topics.Arbitration(), string(b))
	}
	wait := a.cfg().ArbitrationWait
	if over >= arbLoudOver {
		wait = arbQuickWait
	}
	a.arb.mu.Lock()
	if old := a.arb.open; old != nil && !old.done { // a second wake while one is open: keep the first
		a.arb.mu.Unlock()
		r.held.drop()
		return
	}
	a.arb.open = r
	louder := false
	for _, s := range a.arb.claims {
		if r.at.Sub(s.recv) < arbRoundSpan && beats(s.arbClaim, mine) {
			louder = true
		}
	}
	r.timer = time.AfterFunc(wait, func() { a.decide(r) })
	a.arb.mu.Unlock()
	a.log.Info("arbitration: claimed", "level", mine.Level, "over_floor", round1(over), "wait", wait)
	if louder {
		a.decide(r)
	}
}

// decide closes the round: the loudest claim of the round wins.
func (a *App) decide(r *arbRound) {
	a.arb.mu.Lock()
	if r.done {
		a.arb.mu.Unlock()
		return
	}
	r.done = true
	r.timer.Stop()
	if a.arb.open == r {
		a.arb.open = nil
	}
	winner := r.mine
	n := 0
	for _, s := range a.arb.claims {
		if s.recv.Sub(r.at) > -arbRoundSpan && s.recv.Sub(r.at) < arbRoundSpan {
			n++
			if beats(s.arbClaim, winner) {
				winner = s.arbClaim
			}
		}
	}
	a.arb.mu.Unlock()
	if winner.ID != r.mine.ID {
		r.held.drop()
		a.log.Info("arbitration: lost", "to", winner.ID, "their_level", winner.Level, "mine", r.mine.Level)
		a.moment("voice", "arbitration_lost", 0)
		return
	}
	a.log.Info("arbitration: won", "level", r.mine.Level, "others", n)
	a.startWakeWith(r.held)
}

// beats: c is louder than d, or as loud with the lower id.
func beats(c, d arbClaim) bool {
	if math.Abs(c.Level-d.Level) <= arbTieDB {
		return c.ID < d.ID
	}
	return c.Level > d.Level
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
