package app

import (
	"encoding/binary"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/display"
	"github.com/celebeing/yastationd/yastationd/internal/wyoming"
)

// Voice timers: HA owns them (set by voice through the Assist pipeline) and forwards
// timer-started/updated/cancelled/finished to the satellite; we show the soonest active one
// as a matrix countdown and ring an alarm on finish.

const (
	alarmCycles    = 8 // ~20 s of beeping unless a touch key stops it
	alarmRate      = 22050
	alarmToneHz    = 880
	alarmAmplitude = 12000

	// A paused timer blinks its frozen remainder this long, then the clock comes back (the
	// timer stays paused in HA; a resume arrives as timer-updated and shows it again). Without
	// this the display froze for good, e.g. when the assistant mapped «останови таймер» to
	// HassPauseTimer instead of a cancel.
	pausedShowFor = 30 * time.Second
)

type voiceTimer struct {
	deadline time.Time     // when active
	left     time.Duration // frozen remaining time when paused
	active   bool
}

type timers struct {
	mu    sync.Mutex
	m     map[string]*voiceTimer
	alarm chan struct{} // closed to stop the running alarm; nil when idle
}

func (a *App) onTimerEvent(ev wyoming.Event) {
	t := &a.timers
	id := ev.String("id")
	now := time.Now()
	t.mu.Lock()
	if t.m == nil {
		t.m = map[string]*voiceTimer{}
	}
	switch ev.Type {
	case wyoming.TypeTimerStarted:
		left := time.Duration(ev.Int("total_seconds", 0)) * time.Second
		t.m[id] = &voiceTimer{deadline: now.Add(left), left: left, active: true}
	case wyoming.TypeTimerUpdated:
		// total_seconds is the time left at the moment of the update.
		left := time.Duration(ev.Int("total_seconds", 0)) * time.Second
		active, _ := ev.Data["is_active"].(bool)
		t.m[id] = &voiceTimer{deadline: now.Add(left), left: left, active: active}
	case wyoming.TypeTimerCancelled:
		delete(t.m, id)
	case wyoming.TypeTimerFinished:
		delete(t.m, id)
		t.mu.Unlock()
		a.log.Info("timer finished", "id", id)
		a.refreshTimerDisplay()
		a.startAlarm()
		return
	}
	t.mu.Unlock()
	a.log.Info("timer", "event", ev.Type, "id", id, "seconds", ev.Int("total_seconds", -1))
	a.refreshTimerDisplay()
}

// refreshTimerDisplay shows the soonest active timer, else the first paused one frozen and
// blinking for pausedShowFor.
func (a *App) refreshTimerDisplay() {
	if a.front == nil {
		return
	}
	t := &a.timers
	t.mu.Lock()
	var active, paused []*voiceTimer
	for _, vt := range t.m {
		if vt.active {
			active = append(active, vt)
		} else {
			paused = append(paused, vt)
		}
	}
	t.mu.Unlock()
	switch {
	case len(active) > 0:
		sort.Slice(active, func(i, j int) bool { return active[i].deadline.Before(active[j].deadline) })
		a.matrixStack.Push("timer", display.PrioScenario, 0, a.front.Countdown(active[0].deadline, a.brightness))
	case len(paused) > 0:
		left := paused[0].left
		a.matrixStack.Push("timer", display.PrioScenario, pausedShowFor, func(now time.Time, _ display.FrontFrame) (display.FrontFrame, bool) {
			if now.UnixMilli()/500%2 == 1 {
				return display.FrontFrame{}, false
			}
			f, _ := a.front.Countdown(now.Add(left), a.brightness)(now, display.FrontFrame{})
			return f, false
		})
	default:
		a.matrixStack.Remove("timer")
	}
}

// startAlarm beeps and blinks until alarmCycles run out or stopAlarm is called.
func (a *App) startAlarm() {
	t := &a.timers
	t.mu.Lock()
	if t.alarm != nil {
		t.mu.Unlock()
		return
	}
	stop := make(chan struct{})
	t.alarm = stop
	t.mu.Unlock()

	a.ringStack.Push("alarm", display.PrioError, 0, display.Blink(display.Amber, 400*time.Millisecond))
	if c, ok := a.frontAnim("ringing"); ok {
		a.matrixStack.Push("alarm", display.PrioLocal, 0, c.Play(true, false, a.brightness))
	} else if a.front != nil {
		a.matrixStack.Push("alarm", display.PrioLocal, 0, func(now time.Time, _ display.FrontFrame) (display.FrontFrame, bool) {
			if now.UnixMilli()/400%2 == 0 {
				return a.front.Text("00:00", a.brightness()), false
			}
			return display.FrontFrame{}, false
		})
	}
	go func() {
		defer a.endAlarm(stop)
		a.unmute()
		pcm := beepPattern()
		for i := 0; i < alarmCycles; i++ {
			w, done, err := a.player.Stream(alarmRate, 1)
			if err != nil {
				a.log.Warn("alarm: aplay", "err", err)
				return
			}
			w.Write(pcm)
			w.Close()
			select {
			case <-stop:
				a.player.Stop()
				return
			case <-done:
			}
			select {
			case <-stop:
				return
			case <-time.After(time.Second):
			}
		}
	}()
}

func (a *App) endAlarm(stop chan struct{}) {
	t := &a.timers
	t.mu.Lock()
	if t.alarm == stop {
		t.alarm = nil
	}
	t.mu.Unlock()
	a.ringStack.Remove("alarm")
	a.matrixStack.Remove("alarm")
}

// stopAlarm silences a ringing alarm; reports whether one was ringing (so the key press that
// stopped it is not also treated as a volume change).
func (a *App) stopAlarm() bool {
	t := &a.timers
	t.mu.Lock()
	stop := t.alarm
	t.alarm = nil
	t.mu.Unlock()
	if stop == nil {
		return false
	}
	close(stop)
	a.ringStack.Remove("alarm")
	a.matrixStack.Remove("alarm")
	return true
}

// beepPattern is three 150 ms beeps with 100 ms gaps, s16le mono at alarmRate.
func beepPattern() []byte {
	const on, off = 150 * time.Millisecond, 100 * time.Millisecond
	samples := func(d time.Duration) int { return int(d.Seconds() * alarmRate) }
	var out []byte
	for b := 0; b < 3; b++ {
		n := samples(on)
		for i := 0; i < n; i++ {
			env := 1.0 // 5 ms fade in/out against clicks
			if fade := samples(5 * time.Millisecond); i < fade {
				env = float64(i) / float64(fade)
			} else if i > n-fade {
				env = float64(n-i) / float64(fade)
			}
			v := int16(alarmAmplitude * env * math.Sin(2*math.Pi*alarmToneHz*float64(i)/alarmRate))
			out = binary.LittleEndian.AppendUint16(out, uint16(v))
		}
		out = append(out, make([]byte, 2*samples(off))...)
	}
	return out
}
