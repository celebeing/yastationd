package app

import (
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/board"
	"github.com/celebeing/yastationd/yastationd/internal/config"
	"github.com/celebeing/yastationd/yastationd/internal/hw"
)

// persistState is the HA-set state that survives a restart (written to Config.StateFile).
type persistState struct {
	RingOn          bool    `json:"ring_on"`
	RingRGB         hw.RGB  `json:"ring_rgb"`
	RingBright      int     `json:"ring_bright"` // 0..255
	RingEffect      string  `json:"ring_effect"` // one of ringEffects; "" = none
	AmbilightOn     bool    `json:"ambilight_on"`
	AmbilightRGBW   hw.RGBW `json:"ambilight_rgbw"`
	AmbilightBright int     `json:"ambilight_bright"` // 0..255
	// Streamed effects on the ambilight (DDP): taken at all, and their brightness 1..255.
	AmbilightFx       bool `json:"ambilight_fx"`
	AmbilightFxBright int  `json:"ambilight_fx_bright"`
	// The music effect drawn from the spectrum while music plays (musicFxNames).
	AmbilightFxEffect string `json:"ambilight_fx_effect"`
	Mode              string `json:"mode"` // clock|off|message
	Message           string `json:"message"`
	TextSecs          int    `json:"text_secs"`    // show_text duration
	AnimSecs          int    `json:"anim_secs"`    // ring_animation duration
	FollowUp          string `json:"follow_up"`    // dialog mode: off|question|always
	Volume            int    `json:"volume"`       // Master 0..255; -1 = never set (leave the boot value)
	ClockAuto         bool   `json:"clock_auto"`   // clock brightness follows the light sensor
	ClockBright       int    `json:"clock_bright"` // clock brightness 1..255: fixed when not auto, else the brightest auto goes
	// ClockMax: ClockBright is also the auto maximum (since 2026-10-28). A file from before has
	// it false: with auto on, its level was unused, so it becomes 255 (auto reached that).
	ClockMax    bool `json:"clock_bright_is_max,omitempty"`
	MusicVolume int  `json:"music_volume"` // Music Assistant player volume 0..100 (volume "separate")
	// VoiceIgnoreGuests: voice ID drops requests from voices the household doesn't know.
	VoiceIgnoreGuests bool `json:"voice_ignore_guests,omitempty"`
}

func defaultState(ringBright, ambilightBright, clockBright int, clockAuto bool) persistState {
	return persistState{
		RingOn:          false,
		RingRGB:         hw.RGB{R: 255, G: 255, B: 255},
		RingBright:      ringBright,
		AmbilightOn:     false,
		AmbilightRGBW:   hw.RGBW{W: 255},
		AmbilightBright: ambilightBright,
		AmbilightFx:     true,
		// Dim: the strip lights the wall behind the speaker like a floodlight.
		AmbilightFxBright: 64,
		AmbilightFxEffect: "rain",
		Mode:              "clock",
		TextSecs:          10,
		AnimSecs:          10,
		FollowUp:          "question",
		Volume:            -1,
		MusicVolume:       100,
		ClockAuto:         clockAuto,
		ClockBright:       max(clockBright, 1),
		ClockMax:          true,
	}
}

// initialState is the first-start state from the config's initial values.
func initialState(cfg config.Config, prof board.Profile) persistState {
	s := defaultState(cfg.RingBrightness, cfg.AmbilightBrightness, cfg.ClockBrightness, cfg.AutoBrightness)
	if m := prof.Matrix.DefaultMode; m != "" {
		s.Mode = m
	}
	return s
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
		loaded.ClockMax = false // only what the file says
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
			if loaded.AmbilightFxBright <= 0 { // a file from before the effects
				loaded.AmbilightFx, loaded.AmbilightFxBright = dflt.AmbilightFx, dflt.AmbilightFxBright
			}
			if loaded.AmbilightFxEffect == "" {
				loaded.AmbilightFxEffect = dflt.AmbilightFxEffect
			}
			if loaded.FollowUp == "" {
				loaded.FollowUp = dflt.FollowUp
			}
			if !loaded.ClockMax {
				if loaded.ClockAuto {
					loaded.ClockBright = 255
				}
				loaded.ClockMax = true
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
