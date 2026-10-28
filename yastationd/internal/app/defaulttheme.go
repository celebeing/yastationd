package app

import (
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/display"
)

// The moments and the default theme (docs/asset-packs.md): every event the speaker reacts to is a
// moment, and what the CFW does for it out of the box is listed here (and, per display kind, in
// momentDefaults). Code fires a moment and ends a held one; it never draws or plays a reaction
// itself. What code still draws is a reading, not a reaction: the volume level, a timer's
// countdown, a call's duration, the hold of the setup gesture, a track title, a video, HA's
// commands.

// momentInfo is one moment: its name and when it fires (both shown in the web app's editor), and
// where it shows: its slot ("" = "moment:<name>"), the front's and the ring's priority (0 =
// PrioExpression; the ring's 0 = as the front's), how long (0 = as long as its show, or until the
// code ends it), and when its layers yield to what is below (nil = never).
type momentInfo struct {
	Name     string `json:"name"`
	When     string `json:"when"`
	slot     string
	prio     int
	ringPrio int
	ttl      time.Duration
	yield    func(a *App) bool
}

// momentCatalog: the moments the app announces, in the order the web app lists them. The voice
// stages (listening … dialog_end) are fired in the voice slot by the pipeline's own code.
var momentCatalog = []momentInfo{
	{Name: "startup", When: "once per device boot"},
	{Name: "booting", When: "from start until HA is reached (held)", slot: lifeSlot, prio: display.PrioDegraded},
	{Name: "boot_failed", When: "HA not reached within boot_connect_timeout (held until it is)", slot: lifeSlot, prio: display.PrioDegraded},
	{Name: "listening", When: "the wake word or the voice key: the speaker listens"},
	{Name: "thinking", When: "the request went to the assistant: waiting for the answer"},
	{Name: "speaking", When: "the answer plays"},
	{Name: "waiting", When: "the answer takes long: the speaker still waits for it"},
	{Name: "dialog_end", When: "an answer played and no follow-up comes"},
	{Name: "arbitration_lost", When: "another speaker heard the wake word louder and answers"},
	{Name: "voice_error", When: "the voice pipeline failed (HA's error, no STT, a wedged satellite)", slot: "voice", ttl: errorTTL},
	{Name: "voice_muted", When: "a voice attempt while the mic is muted", slot: "voice", ttl: errorTTL},
	{Name: "voice_unavailable", When: "a voice attempt finds no HA to talk to", slot: "voice", prio: display.PrioError, ttl: 2 * time.Second},
	{Name: "voice_cancelled", When: "«стоп» cancelled the session or the pending reply", slot: "voice"},
	{Name: "reply_timeout", When: "no reply within reply_timeout", slot: "voice", ttl: errorTTL},
	{Name: "wake_busy", When: "the wake word while a reply is pending (wake_while_waiting = ignore)", slot: "busy"},
	{Name: "media_pause", When: "music paused"},
	{Name: "media_resume", When: "music resumed"},
	{Name: "volume_change", When: "a volume key moved the level (not over music; volume_beep)"},
	{Name: "volume_edge", When: "a volume key pushed past the bottom or the top"},
	{Name: "announce", When: "before an HA announcement (announce_chime); the announcement waits for its sounds"},
	{Name: "mic_muted", When: "the mic-mute switch is on (held until it is off)", slot: "mute", prio: display.PrioLocal},
	{Name: "mic_unmuted", When: "the mic-mute switch turned off"},
	{Name: "ha_lost", When: "the link to HA or a probe is lost after start (held until back)", slot: "degraded", prio: display.PrioDegraded},
	{Name: "ha_back", When: "the link to HA is back"},
	{Name: "power_problem", When: "no healthy USB-C power contract (once per boot)"},
	{Name: "call_outgoing", When: "this speaker calls another (held until answered or ended)", slot: "intercom", prio: prioCallFront, ringPrio: display.PrioCall},
	{Name: "call_incoming", When: "another speaker calls this one (held until answered or ended)", slot: "intercom", prio: prioCallFront, ringPrio: display.PrioCall, yield: (*App).icWindow},
	{Name: "call_connected", When: "an intercom call is up (held until it ends); no sound here = the announce chime", slot: "intercom", prio: prioCallFront, ringPrio: display.PrioCall, yield: (*App).icInCallYield},
	{Name: "call_ended", When: "an intercom call that was up ended", slot: "intercom-end", ringPrio: display.PrioCall, ttl: 600 * time.Millisecond},
	{Name: "call_cancelled", When: "a call ended before it was up (hung up while ringing)", slot: "intercom-end", ringPrio: display.PrioCall, ttl: 600 * time.Millisecond},
	{Name: "call_missed", When: "a call rang out here unanswered (the caller gave up, or the ring timed out)"},
	{Name: "call_busy", When: "a call did not happen: refused, no answer, no audio", slot: "voice", ttl: errorTTL},
	{Name: "call_failed", When: "a call could not be placed (unknown room, a call already on)", slot: "voice", ttl: errorTTL},
	{Name: "timer_started", When: "an HA timer started on this speaker"},
	{Name: "timer_finished", When: "a timer rings (held until stopped or done); no sound here = the built-in beep", slot: "alarm", prio: display.PrioLocal, ringPrio: display.PrioError},
	{Name: "wifi_setup", When: "Wi-Fi setup mode is on (held until it ends)", slot: "wifisetup", prio: display.PrioSetup},
	{Name: "wifi_setup_failed", When: "Wi-Fi setup mode could not start", slot: "voice", ttl: errorTTL},
}

// momentByName indexes momentCatalog.
var momentByName = func() map[string]momentInfo {
	m := map[string]momentInfo{}
	for _, i := range momentCatalog {
		m[i.Name] = i
	}
	return m
}()

// held is a one-step show that stays until the code ends its moment.
func held(st scenarioStep) show {
	st.Hold = true
	return show{Steps: []scenarioStep{st}}
}

func step(st scenarioStep) show { return show{Steps: []scenarioStep{st}} }

// defaultTheme: the default theme's moments on every display kind. A moment listed with no shows
// does nothing by default; a theme gives it something. A sound name may list fallbacks, "a|b".
var defaultTheme = map[string][]show{
	"booting":           {held(scenarioStep{Ring: "booting", Anim: "booting", Else: "blank"})},
	"boot_failed":       {held(scenarioStep{Ring: "boot_failed", Anim: "booting", Else: "blank"})},
	"voice_error":       {{Ref: "error"}},
	"voice_muted":       {{Ref: "mute"}},
	"voice_unavailable": {step(scenarioStep{Ring: "unavailable", Phrase: "something_went_wrong", Ms: 2000})},
	"voice_cancelled":   {{Ref: "blink"}},
	"reply_timeout":     {{Ref: "sad"}},
	"wake_busy":         {{Ref: "busy"}},
	"volume_change":     {{Ref: "sound:volume_sound"}},
	"volume_edge":       {},
	"announce":          {{Ref: "sound:notification_sound"}},
	"mic_muted":         {held(scenarioStep{Ring: "mute"})},
	"mic_unmuted":       {},
	"ha_lost":           {held(scenarioStep{Ring: "degraded"})},
	"ha_back":           {},
	"power_problem":     {{Ref: "phrase:no_power"}},
	"call_outgoing":     {held(scenarioStep{Ring: "calling", Anim: "call_outgoing", Sound: "calls_ringing_outgoing"})},
	"call_incoming":     {held(scenarioStep{Ring: "call_ringing", Anim: "call_incoming", Sound: "calls_ringing_incoming|notification_sound"})},
	"call_connected": {{Steps: []scenarioStep{ // the sound once, then the ring held
		{Ring: "in_call", Sound: "calls_connected"}, {Ring: "in_call", Hold: true}}}},
	"call_ended":        {step(scenarioStep{Ring: "call_end", Sound: "calls_ended", Ms: 600})},
	"call_cancelled":    {step(scenarioStep{Ring: "call_end", Ms: 600})},
	"call_busy":         {step(scenarioStep{Express: "error", Sound: "calls_busy|calls_ended", Ms: int(errorTTL / time.Millisecond)})},
	"call_failed":       {{Ref: "error"}},
	"call_missed":       {},
	"timer_started":     {},
	"timer_finished":    {held(scenarioStep{Ring: "alarm", Anim: "ringing", Else: "alarm"})},
	"wifi_setup":        {held(scenarioStep{Ring: "setup", Anim: "setup", Else: "setup"})},
	"wifi_setup_failed": {{Ref: "error"}},
}
