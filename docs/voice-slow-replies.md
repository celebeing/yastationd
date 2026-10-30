# Slow replies: waiting, cancelling, interrupting

Status (2026-10-24): implemented and checked on a bergamot unit against a test bench in HA (an
Assist sentence that replies after a set delay).

## How HA behaves (measured, HA 2026.9, Wyoming satellite)

- After the transcript HA is silent until the reply: intent-end, tts and run-end arrive within a
  millisecond of each other, the TTS audio ~0.13 s later. Nothing in between.
- **HA has no timeout of its own** for the reply stage or the satellite pipeline: it waited the
  full 10 / 30 / 60 / 120 s of the bench and sent no error. The only thing that ends a wait is the
  connection: HA pings 2 s after each pong and drops the connection when 5 s pass without any
  event from us (constants, not settings).
- **A new `run-pipeline` while HA still runs one wedges its satellite** (reproduced twice of two):
  the new run cancels the old one, the old run's `run-end` lands 2–3 ms after the new run started
  and clears the satellite's "pipeline running" flag; HA then discards the new run's audio and
  `audio-stop` as unexpected events, and the new run hangs in STT with no timeout. Every later run
  cancels the dead one the same way, so it never recovers on its own. Any drop of the connection
  clears it.
- **Dropping the connection from our side is a clean cancel:** HA sees the socket close at once,
  cancels the run and reconnects 3 s later, in a working state.
- **Cancelling never undoes HA's actions:** an automation or tool call already started runs to the
  end; only the spoken reply is lost.

## What the speaker does

- **A reply is pending** from the transcript until HA's audio starts, a pipeline error, a new
  session, a pause or a disconnect. Our own deadline is `reply_timeout` (default 90 s).
- **Indication:** "thinking" on the ring and the front display for the whole wait; after 10 s the
  slower, dimmer "waiting". On the deadline the speaker drops the connection (the clean cancel) and
  shows a sad face.
- **Music** stays ducked for the whole wait, so music HA starts before its spoken reply comes in
  quietly and lets the reply through.
- **A late reply is a reply:** audio from HA while a reply is pending gets no announce chime.
- **«стоп» / «хватит» / «отмена»** while a reply is pending cancel it (connection drop) with a
  blink. HA's action still completes.
- **A wake word while a reply is pending** (`wake_while_waiting`):
  - `interrupt` (default): the speaker buffers the speech from the wake word on, drops the
    connection, and starts the new session as soon as HA is back (~3 s), streaming the buffered
    speech first. A new `run-pipeline` is never sent over a running one. If HA is not back within
    8 s, an error.
  - `ignore`: a short "busy" blink; the wait goes on.
- **Wedge watchdog:** if the speaker heard speech in a session (loud level ≥ 12 dB over the floor)
  but HA sent no `voice-started` within 8 s, it drops the connection to clear a wedged satellite.
- **HA drops us** (ping timeout) with a reply pending: the wait ends with an error indication.

Code: `yastationd/internal/app/voice.go` (pending reply, cancel, interrupt, watchdog),
`wakeword.go` (the policies), `satellite.go`.

## Config (yastationd.conf)

```
reply_timeout      = 90         # s, 10..600: our deadline for HA's reply
wake_while_waiting = interrupt  # or ignore
```

## Not done

- A spoken "не дождалась ответа" on the deadline: needs a pre-rendered phrase (the voice bank:
  [wishlist.md](wishlist.md), "A bank of pre-rendered phrases"). Today it is a sad face.
- An optional soft "still working" sound during long waits.
