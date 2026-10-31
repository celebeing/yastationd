package app

import (
	"sync"
	"time"
)

// The sync's fuse (docs/settings.md, "Sync"): whatever the cause, speakers must not trade a shared
// item without end, since every change rewrites the config on flash. The sign of that is not speed
// (a slider dragged on one speaker is fast and harmless) but authors taking turns: A, B, C, A... on
// one item. So the fuse counts changes of an item whose author differs from the change before,
// with a high ceiling on any changes at all, for a speaker that loops by itself. When it blows, this
// speaker stops taking and sending shared items for a while; then it takes the latest values.
const (
	fuseWindow    = time.Minute
	fuseSwitches  = 10  // author changes of one item in fuseWindow
	fuseChanges   = 120 // changes of one item in fuseWindow, by anyone
	fusePause     = 10 * time.Minute
	fusePauseMax  = time.Hour
	fuseForgetsIn = 2 * time.Hour // a quiet time after which the next pause is the short one again
)

type fuseEvent struct {
	at       time.Time
	switched bool
}

type syncFuse struct {
	mu    sync.Mutex
	items map[string][]fuseEvent
	last  map[string]string // the author of an item's last change
	until time.Time         // paused until then
	pause time.Duration     // the last pause
	tripT time.Time
}

func (f *syncFuse) paused(now time.Time) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return now.Before(f.until)
}

// note records a change of an item by an author; it returns the pause when this one blew the fuse.
func (f *syncFuse) note(key, by string, now time.Time) time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.items == nil {
		f.items, f.last = map[string][]fuseEvent{}, map[string]string{}
	}
	prev, seen := f.last[key]
	f.last[key] = by
	ev := f.items[key]
	keep := ev[:0]
	for _, e := range ev {
		if now.Sub(e.at) < fuseWindow {
			keep = append(keep, e)
		}
	}
	keep = append(keep, fuseEvent{now, seen && prev != by})
	f.items[key] = keep
	switches := 0
	for _, e := range keep {
		if e.switched {
			switches++
		}
	}
	if switches <= fuseSwitches && len(keep) <= fuseChanges {
		return 0
	}
	if f.pause == 0 || now.Sub(f.tripT) > fuseForgetsIn {
		f.pause = fusePause
	} else if f.pause *= 2; f.pause > fusePauseMax {
		f.pause = fusePauseMax
	}
	f.tripT, f.until = now, now.Add(f.pause)
	clear(f.items)
	clear(f.last)
	return f.pause
}

// fuseNote counts a change; true: the fuse blew on it, it goes no further.
func (a *App) fuseNote(key, by string) bool {
	pause := a.shared.fuse.note(key, by, a.shared.clock())
	if pause == 0 {
		return false
	}
	a.log.Warn("sync: speakers keep changing a shared item, pausing the sync", "item", key, "for", pause.String())
	if a.shared.afterPause != nil {
		a.shared.afterPause(pause)
	} else {
		time.AfterFunc(pause, a.syncResume)
	}
	return true
}

// syncResume takes the shared values that came in during a pause.
func (a *App) syncResume() {
	if a.shared.fuse.paused(a.shared.clock()) {
		return
	}
	a.shared.mu.Lock()
	items := make(map[string]sharedMsg, len(a.shared.items))
	for k, m := range a.shared.items {
		items[k] = m
	}
	a.shared.mu.Unlock()
	a.log.Info("sync: resuming after a pause", "items", len(items))
	for key, m := range items {
		kind, item := splitShared(key)
		a.takeShared(kind, item, m)
	}
}
