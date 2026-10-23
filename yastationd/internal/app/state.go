package app

import (
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/hw"
)

// persistState is the HA-set state that survives a restart (written to Config.StateFile).
type persistState struct {
	RingOn      bool   `json:"ring_on"`
	RingRGB     hw.RGB `json:"ring_rgb"`
	RingBright  int    `json:"ring_bright"` // 0..255
	RingEffect  string `json:"ring_effect"` // one of ringEffects; "" = none
	Mode        string `json:"mode"`        // clock|off|message
	Message     string `json:"message"`
	TextSecs    int    `json:"text_secs"`    // show_text duration
	AnimSecs    int    `json:"anim_secs"`    // ring_animation duration
	FollowUp    string `json:"follow_up"`    // dialog mode: off|question|always
	Volume      int    `json:"volume"`       // Master 0..255; -1 = never set (leave the boot value)
	ClockAuto   bool   `json:"clock_auto"`   // clock brightness follows the light sensor
	ClockBright int    `json:"clock_bright"` // manual clock brightness 1..255
}

func defaultState(ringBright, clockBright int, clockAuto bool) persistState {
	return persistState{
		RingOn:      false,
		RingRGB:     hw.RGB{R: 255, G: 255, B: 255},
		RingBright:  ringBright,
		Mode:        "clock",
		TextSecs:    10,
		AnimSecs:    10,
		FollowUp:    "question",
		Volume:      -1,
		ClockAuto:   clockAuto,
		ClockBright: max(clockBright, 1),
	}
}

// stateFlushDelay coalesces bursts (volume key repeats, slider drags) into one flash write.
const stateFlushDelay = 2 * time.Second

type stateStore struct {
	mu    sync.Mutex
	path  string
	s     persistState
	timer *time.Timer // pending flush
}

func newStateStore(path string, dflt persistState) *stateStore {
	st := &stateStore{path: path, s: dflt}
	if b, err := os.ReadFile(path); err == nil {
		// Unmarshal over the defaults so fields added since the file was written keep theirs.
		// (Volume is absent in older files and stays -1 = untouched.)
		loaded := dflt
		if json.Unmarshal(b, &loaded) == nil {
			if loaded.ClockBright <= 0 {
				loaded.ClockBright = dflt.ClockBright
			}
			if loaded.TextSecs <= 0 {
				loaded.TextSecs = dflt.TextSecs
			}
			if loaded.AnimSecs <= 0 {
				loaded.AnimSecs = dflt.AnimSecs
			}
			if loaded.FollowUp == "" {
				loaded.FollowUp = dflt.FollowUp
			}
			st.s = loaded
		}
	}
	return st
}

func (st *stateStore) get() persistState {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.s
}

// update mutates the state under lock and schedules a (debounced) write to flash.
func (st *stateStore) update(fn func(*persistState)) persistState {
	st.mu.Lock()
	defer st.mu.Unlock()
	fn(&st.s)
	if st.path != "" && st.timer == nil {
		st.timer = time.AfterFunc(stateFlushDelay, st.flush)
	}
	return st.s
}

// flush writes the state now (atomic rename); also called on shutdown.
func (st *stateStore) flush() {
	st.mu.Lock()
	if st.timer != nil {
		st.timer.Stop()
		st.timer = nil
	}
	s, path := st.s, st.path
	st.mu.Unlock()
	if path == "" {
		return
	}
	if b, err := json.Marshal(s); err == nil {
		tmp := path + ".tmp"
		if os.WriteFile(tmp, b, 0o644) == nil {
			os.Rename(tmp, path)
		}
	}
}
