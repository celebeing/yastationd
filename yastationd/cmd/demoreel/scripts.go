package main

import (
	"sort"
	"time"
)

// step sends a command (none when cmd is empty), then waits.
type step struct {
	wait    time.Duration
	cmd     string
	payload any
}

const sec = time.Second

func clip(name string) map[string]any {
	return map[string]any{"surface": "matrix", "op": "clip", "args": map[string]any{"name": name}}
}

func expr(name string, ms int) map[string]any { return map[string]any{"name": name, "ttl_ms": ms} }

var scripts = map[string][]step{
	// Station 3. "video" steps play pieces of a converted video from -media (docs/led-preview.md,
	// "Demo reels"): ba1..ba3, Bad Apple.
	"orion": {
		{3 * sec, "display_mode", "clock"},
		{6500 * time.Millisecond, "video", "ba1.led.gz"},
		{4 * sec, "display_mode", "clock_date"},
		{sec, "display_mode", "clock"},
		{3500 * time.Millisecond, "express", expr("eyes", 3000)},
		{3 * sec, "express", expr("listening", 2500)},
		{3 * sec, "express", expr("thinking", 2500)},
		{3 * sec, "express", expr("speaking", 2500)},
		{2500 * time.Millisecond, "express", expr("wink", 2000)},
		{6500 * time.Millisecond, "video", "ba2.led.gz"},
		{3 * sec, "express", expr("happy", 2500)},
		{2500 * time.Millisecond, "display", clip("media/Like")},
		{2500 * time.Millisecond, "display", clip("timer/timer_bell")},
		{0, "text_duration", "6"},
		{6300 * time.Millisecond, "show_text", "Привет из yastationd"},
		{6500 * time.Millisecond, "video", "ba3.led.gz"},
		{2 * sec, "", nil},
	},
	// The minis' 7-segment clock.
	"seg7": {
		{3 * sec, "display_mode", "clock"},
		{3500 * time.Millisecond, "express", expr("eyes", 3000)},
		{3 * sec, "express", expr("listening", 2500)},
		{3 * sec, "express", expr("thinking", 2500)},
		{3 * sec, "express", expr("speaking", 2500)},
		{2500 * time.Millisecond, "express", expr("wink", 2000)},
		{3 * sec, "express", expr("happy", 2500)},
		{3 * sec, "express", expr("sad", 2500)},
		{3 * sec, "express", expr("angry", 2500)},
		{3 * sec, "express", expr("sleep", 2500)},
		{3 * sec, "volume", "7"},
		{2 * sec, "volume", "5"},
		{0, "text_duration", "6"},
		{6300 * time.Millisecond, "show_text", "ПРИВЕТ ИЗ YASTATIOND"},
		{4 * sec, "display", map[string]any{"surface": "matrix", "op": "countdown", "args": map[string]any{"seconds": 63}}},
		{2 * sec, "display", map[string]any{"surface": "matrix", "op": "clear"}},
	},
}

func scriptNames() []string {
	var n []string
	for k := range scripts {
		n = append(n, k)
	}
	sort.Strings(n)
	return n
}
