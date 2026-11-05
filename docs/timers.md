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
  built-in beep unless the theme gives a sound) for about 20 s.
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
