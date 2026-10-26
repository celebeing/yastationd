package app

import (
	"encoding/binary"
	"math"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/hw"
)

// Room sound sensors (the raw inputs of "someone is asleep here", docs/wishlist.md): the level of
// one raw mic channel — before the VQE and its AGC, so a quiet room reads quiet — as three
// per-minute numbers for HA's history. Only levels leave the speaker, never audio.
//
//   - sound_floor: the 10th percentile of the minute's 20 ms levels (the room's background);
//   - sound_level: the minute's energy average (Leq);
//   - sound_activity: the share of 20 ms blocks more than roomActiveDB above that floor. The
//     blocks are short so a single click (a mouse, a key, a step) lifts one: a click lasts
//     5-20 ms and a 100 ms block averaged it into the floor.
//
// Blocks while our own speaker plays or the mic is muted are left out; a minute with too few
// others publishes "None" (unknown in HA). A value is re-published only when it moved
// (roundings: 1 dB, 1 %) or every roomRefresh, so HA's recorder gets a few rows an hour in a
// quiet room.

const (
	roomBlock    = 20 * time.Millisecond
	roomMinute   = time.Minute
	roomActiveDB = 6.0
	roomMinShare = 0.25 // of a minute's blocks needed for a value
	roomRefresh  = 15 * time.Minute
	roomSilentDB = -96.0 // a block of digital silence
)

// roomRate is the rate the level is taken at: every rate/roomRate-th capture sample, with no
// low-pass first. Aliasing folds the high band down but keeps its power, and a level is all we
// want; at orion's 96 kHz it is a sixth of the work.
const roomRate = 16000

type roomSound struct {
	rate    int
	channel int
	fsz     int
	stride  int
	hpf     *hw.Highpass
	skip    func() bool // our playback or mic mute: the block is not the room

	mu     sync.Mutex
	sum    float64 // the current block's energy
	n      int
	levels []float64                 // the minute's block levels (dBFS)
	total  int                       // blocks this minute, kept or not
	recent [recentBlocks]recentLevel // every block of the last seconds, ring (wake arbitration)
	head   int
	floor  float64 // the last minute's floor (dBFS), 0 = none yet
}

// recentBlocks: the last 2 s of 20 ms blocks.
const recentBlocks = 100

type recentLevel struct {
	at   time.Time
	dBFS float64
}

func newRoomSound(rate, channels, channel int, skip func() bool) *roomSound {
	stride := max(rate/roomRate, 1)
	return &roomSound{rate: rate / stride, channel: channel, fsz: 2 * channels, stride: stride,
		hpf: hw.NewHighpass(150, float64(rate/stride)), skip: skip}
}

// feed takes raw interleaved capture frames (the mic's SubscribeRaw).
func (r *roomSound) feed(raw []byte) {
	per := int(float64(r.rate) * roomBlock.Seconds())
	r.mu.Lock()
	defer r.mu.Unlock()
	for off := 2 * r.channel; off+1 < len(raw); off += r.fsz * r.stride {
		s := float64(r.hpf.Push(int16(binary.LittleEndian.Uint16(raw[off:]))))
		r.sum += s * s
		r.n++
		if r.n == per {
			r.total++
			lv := dbfs(r.sum / float64(r.n))
			r.recent[r.head] = recentLevel{time.Now(), lv}
			r.head = (r.head + 1) % recentBlocks
			if !r.skip() {
				r.levels = append(r.levels, lv)
			}
			r.sum, r.n = 0, 0
		}
	}
}

// peak is the level of a sound that just ended — the wake word: the energy average of the
// loudest blocks of the last span, in dBFS, and how far it stands above the room's floor
// (0 when no floor is known yet). ok is false with no blocks in the span.
func (r *roomSound) peak(span time.Duration, loudest int) (level, overFloor float64, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	since := time.Now().Add(-span)
	var lv []float64
	for _, b := range r.recent {
		if !b.at.IsZero() && b.at.After(since) {
			lv = append(lv, b.dBFS)
		}
	}
	if len(lv) == 0 {
		return 0, 0, false
	}
	sort.Sort(sort.Reverse(sort.Float64Slice(lv)))
	lv = lv[:min(loudest, len(lv))]
	var e float64
	for _, v := range lv {
		e += math.Pow(10, v/10)
	}
	level = math.Max(10*math.Log10(e/float64(len(lv))), roomSilentDB)
	if r.floor != 0 {
		overFloor = level - r.floor
	}
	return level, overFloor, true
}

// roomStats is one minute's result; ok is false when too little of it was the room.
type roomStats struct {
	floor, level, activity float64
	over                   [3]float64 // the share above the floor by 3, 6 and 10 dB, for tuning
	ok                     bool
}

// take closes the minute.
func (r *roomSound) take() roomStats {
	r.mu.Lock()
	lv, total := r.levels, r.total
	r.levels, r.total = nil, 0
	r.mu.Unlock()
	st := minuteStats(lv, total)
	if st.ok {
		r.mu.Lock()
		r.floor = st.floor
		r.mu.Unlock()
	}
	return st
}

func minuteStats(lv []float64, total int) roomStats {
	if total == 0 || float64(len(lv)) < roomMinShare*float64(total) {
		return roomStats{}
	}
	sorted := append([]float64(nil), lv...)
	sort.Float64s(sorted)
	floor := sorted[len(sorted)/10]
	var e float64
	active := 0
	var over [3]float64
	for _, v := range lv {
		e += math.Pow(10, v/10)
		if v > floor+roomActiveDB {
			active++
		}
		for i, d := range [3]float64{3, 6, 10} {
			if v > floor+d {
				over[i] += 100 / float64(len(lv))
			}
		}
	}
	return roomStats{
		floor:    floor,
		level:    math.Max(10*math.Log10(e/float64(len(lv))), roomSilentDB),
		activity: 100 * float64(active) / float64(len(lv)),
		over:     over,
		ok:       true,
	}
}

// dbfs of a mean square of S16 samples (full scale = a full-scale sine's RMS is about -3).
func dbfs(meanSquare float64) float64 {
	if meanSquare <= 0 {
		return roomSilentDB
	}
	return math.Max(10*math.Log10(meanSquare/(32768*32768)), roomSilentDB)
}

// changeGate re-publishes a sensor only when its shown value changes or it is due a refresh.
type changeGate struct {
	last string
	at   time.Time
}

func (g *changeGate) due(v string, now time.Time) bool {
	if v == g.last && now.Sub(g.at) < roomRefresh {
		return false
	}
	g.last, g.at = v, now
	return true
}

// splOffset: dBFS -> dB SPL for the room sound levels (the unit's, else the board's; 0 = levels
// stay in dBFS).
func (a *App) splOffset() float64 {
	if a.cfg.MicSPLOffset != 0 {
		return a.cfg.MicSPLOffset
	}
	return a.prof.Mic.SPLOffset
}

func (a *App) roomSoundEnabled() bool {
	return a.cfg.RoomSound && a.prof.Mic.Device != ""
}

// micLevelsNeeded: the mic's levels feed the room sensors and the wake arbitration.
func (a *App) micLevelsNeeded() bool {
	return (a.cfg.RoomSound || a.cfg.Arbitration) && a.prof.Mic.Device != ""
}

// roomSoundLoop measures while it runs (it keeps the mic open) and publishes once a minute.
func (a *App) roomSoundLoop(stop <-chan struct{}) {
	r := a.room
	unsub := a.mic.SubscribeRaw(r.feed)
	defer unsub()
	gates := map[string]*changeGate{"sound_floor": {}, "sound_level": {}, "sound_activity": {}}
	t := time.NewTicker(roomMinute)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		st := r.take()
		vals := map[string]string{"sound_floor": "None", "sound_level": "None", "sound_activity": "None"}
		if st.ok {
			off := a.splOffset()
			vals["sound_floor"] = strconv.Itoa(int(math.Round(st.floor + off)))
			vals["sound_level"] = strconv.Itoa(int(math.Round(st.level + off)))
			vals["sound_activity"] = strconv.Itoa(int(math.Round(st.activity)))
			a.log.Debug("room sound", "floor", st.floor, "level", st.level,
				"over3", st.over[0], "over6", st.over[1], "over10", st.over[2])
		}
		if !a.roomSoundEnabled() {
			continue
		}
		now := time.Now()
		for obj, v := range vals {
			if gates[obj].due(v, now) {
				a.publish(a.topics.State(obj), v)
			}
		}
	}
}
