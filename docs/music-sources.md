# Music sources: Music Assistant and a phone over Bluetooth, side by side

Status (2026-11-04): built. Two things besides the assistant play music through a speaker: Music
Assistant's player ([music-sendspin.md](music-sendspin.md)) and, on Station 3 and Mini 3 Pro, a phone
over Bluetooth ([bluetooth.md](bluetooth.md)). Each is a *source* (`internal/app/media.go`), and the
rest of the daemon deals with sources, not with either one:

| | Music Assistant | Bluetooth |
|---|---|---|
| Plays when | its stream is up and the track is not paused | the phone's player (AVRCP) says `playing` |
| Ducks through | `duck` to `sendspin-host` (ramps in its sink) | the board's Bluetooth softvol, down at once, back over ~1 s |
| Pause from the speaker | no (the Sendspin player has no pause) | yes (AVRCP `Pause`) |
| Track title, pause/resume animations | from `META` | from AVRCP's track and status |
| Spectrum under the clock, ambilight music effects | from `LEVELS` | no (the audio does not pass through the daemon; [wishlist.md](wishlist.md)) |

What asks "does music play" asks the sources: a video does not start over music (and stops when music
starts), the volume keys' tick stays quiet over music, the acoustic measurement refuses to start.

## Ducking

One loop (every 100 ms) sets every source's level. While the assistant is busy (a voice session, a
reply, our own playback, a ringing timer, an intercom call) and 1.5 s after, every source is at
`music_duck_db` (default −26 dB). The wake word ducks at once, for 2 s on its own: before
arbitration, voice ID or HA get the speaker's own music, and until the session holds the duck.
Only the sources' own levels move, never the master, so `volume_max` caps them all.

## Two at once: `music_overlap`

Configuration → Music → Music sources (with `music_duck_db`).

- `newest` (the default): the source that started last plays. The other is paused once (a phone) or,
  where it can't be (Music Assistant), muted until the newest stops, and then it comes back by itself.
  A phone paused this way stays paused: press play on it.
- `mix`: both play together, each ducked under the assistant.

## Adding a source

A source implements `mediaSource` (`name`, `playing`, `setDuck`, `pause`) and is registered with
`addMedia` before the loop starts (`App.Run`). Its track and pauses go to the display through
`showMusicTitle` and `musicMoment` (`media_pause` / `media_resume`), like the two above.
