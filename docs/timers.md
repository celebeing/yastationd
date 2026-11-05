# Voice timers, and timers that follow a person

Status (2026-11-06): built. Timers are Home Assistant's; the speaker shows and rings them. A timer a
known voice sets can also count down and ring on that person's own speakers.

## A timer on the speaker that was asked

«Алиса, поставь таймер на 5 минут»: HA's Assist starts the timer for the satellite the request came
from and tells only it, over Wyoming: `timer-started`, `timer-updated` (paused, resumed, changed),
`timer-cancelled`, `timer-finished` (`internal/app/timers.go`).

- **The countdown** of the soonest running timer is on the front display; a paused one blinks its
  frozen remainder for 30 s, then the clock is back.
- **Ringing**: `timer-finished` fires `timer_finished` (held: an amber blink, the ringing clip, the
  built-in beep unless the theme gives a sound) for about 14 s (8 rounds of three beeps and a second of quiet), then stops by itself.
- **Stopping**: any mapped key or the touch ring, «стоп» (the command spotter is armed while the
  speaker makes sound, ringing included; over the ringing a near miss counts, below), or HA's stop
  command.

## Timers that follow a person

`timer_follow` (on; Configuration → Voice → Voice ID). When voice ID took the request for someone
of the household and that person has **their speakers** set in the People tab
([voice-id.md](voice-id.md)), the speaker that was asked (the owner) shares the timer with those
speakers:

- they show the same countdown and ring at the same moment (by their own clocks, NTP-synced; the
  owner's ringing reaches them too, if it comes first);
- a pause, a resume or a cancellation through HA reaches them;
- **a stop on any of them stops it on all**: the key, the touch ring, «стоп» or HA's command, on the
  owner or on any of the places; a ringing that runs out ends everywhere too.

A guest, an unknown voice, a person with no speakers set: the timer stays where it was asked, as
before. The speaker that was asked is never its own place.

**How.** HA keeps the timer on the owner and knows nothing of the others. The owner publishes a
retained message per timer, `yastation/timers/<owner>/<id>`: `owner`, `id`, `person`, `name`,
`places` (device_ids), `state` (`running`, `paused`, `ringing`, `done`), `deadline_ms` or `left_ms`,
`by` (who sent it). Every speaker subscribes to `yastation/timers/+/+` and follows the ones that
name it in `places`; a speaker that boots later picks a running one up from the retained message.
A stop is a `done` from the speaker that stopped; the owner then clears its retained message. Who
asked: the latest known verdict of this speaker within 30 s, waiting up to 4 s for one still on its
way when HA starts the timer (the early verdict usually comes before).

**Not done.** «Отмени таймер» in another room goes to that room's satellite, which HA gave no timer:
HA answers that there is none. Cancelling a followed timer by voice works on the speaker that was
asked.

**HA's Russian reply for a started timer fails on a number said as a word** (seen 2026-11-05, HA
2026.9): «на одну минуту» gives the minutes slot as `одну`, the built-in response template does
`| int` on it and raises, so HA returns `intent-failed` after the timer has started: the timer runs
(and is shared), the speaker shows `voice_error`, and no confirmation is spoken. HA's side (its
`HassStartTimer` response in `custom_sentences`, or upstream in home-assistant/intents); nothing to
fix on the speaker.

**On a Mini 3 the stop words over the ringing went unheard** (2026-11-05, a quiet voice next to
the speaker): the command spotter gave neither a hit nor a near miss in the 14 s; the same model
spots TTS «стоп» offline on that unit, and the same voice's «Алиса» was heard just before. Whether
the voice was too quiet over the beep or the Mini's AGC/AEC under its own beep is the cause is
open: a capture of what the spotter got while ringing would tell.

## «Стоп» while ringing: what the log keeps

While a timer rings, the command spotter's hits and near misses (`SUBHIT`) are logged at info
(`alarm: command heard`, `alarm: near miss`), and every stop with where it came from (`alarm:
stopped by=…`). A «стоп» that did not stop the ringing shows there as a near miss or as nothing at
all (the beep drowned it).

The first log (2026-11-05) showed the miss: a «стоп» over the beep scored just under the command
threshold (a near miss), the second one 2.4 s later a hit. So **while a timer rings, a near miss of
a stop word stops it too** (`стоп`, `хватит`, `отмена`, `выключись`); a wrong one only silences
the ringing.

## Config

```
timer_follow = true   # a known voice's timer also rings on their own speakers (People tab)
```
