# Adding a feature (agent guide)

How the CFW is built and the conventions a new feature follows, so it fits next to the rest
instead of beside it. For a new speaker model, read [porting-a-speaker.md](porting-a-speaker.md)
instead; for what a change must touch outside the code (docs, deploy, config examples, the vendor
repo), the "Changes that ripple" list in [CLAUDE.md](../CLAUDE.md).

**The short version.** A setting is a key in the registry. Every sound, animation, ring effect or
other show the speaker puts on in reaction to something goes through **moments**, our own
abstraction: code names the event, the theme stack decides what plays. An HA entity and its web app control
are one definition. The web app reuses the settings page's widgets and the LED page's painter. A
vendor library runs in a child process. Read the doc of the area first ([docs/index.md](index.md)), and say
in the doc what you built.

## Where things are

| Path | What |
|---|---|
| `yastationd/internal/app/` | the daemon: one `App`, a file per area (`voice.go`, `satellite.go`, `moments.go`, `packs.go`, `intercom.go`, `voiceid.go`…) |
| `yastationd/internal/config/` | the config: `registry.go` (every key), `config.go` (the `Config` struct and defaults) |
| `yastationd/internal/board/profiles/` | per-model hardware profiles ([porting-a-speaker.md](porting-a-speaker.md)) |
| `yastationd/internal/mqtt/` | the broker client, topics (`topics.go`), HA discovery (`discovery.go`) |
| `yastationd/internal/display/`, `hw/` | the display stacks and front renderers; the hardware (LEDs, ALSA, input, sensors) |
| `yastationd/internal/<feature>/` | self-contained logic with its own tests (`intercom`, `voiceid`, `wake`, `wyoming`, `sendspin`…) |
| `spotter-host/` | the C host of the vendor's voice libraries (wake word, VQE, voiceprints), one binary, several modes |
| `web/src/` | the web app (Preact), below |

The web app's files: `app.tsx` (the speakers list, a speaker's page, `GROUPS` and the tabs, the
card), `api.ts` (every API call and its types), `controls.tsx` (`Switch`, `Control` for an
entity, the controls tab), `settings.tsx` (the Configuration pages, `SettingRows`, `ShareSwitch`),
`moments.tsx` (the moments editor), `packs.tsx` (packs, sounds, the import, the previews),
`assets.tsx` (videos, the browser-side audio and video conversion), `voices.tsx` (the People tab),
`extra.tsx` (the LED page and its animation browser, the log, Wi-Fi, About), `ledframe.ts` (the
front display painter), `seg7.ts` (the 7-segment shapes), `style.css` (every style; tokens on
top). The only check is the type check in the build (`tools/build.sh web`); there are no web
tests, so look at the page in a browser.

## Settings: a key in the registry

Every config key is described **once**, in `internal/config/registry.go`; that one entry gives the
parser, the settings page row (label, control, paragraph), the API and the sync between speakers.
The value itself is a field of `Config` with its default (`internal/config/config.go`), which the
registry entry points at; nothing else needs touching.
The rules (group, label, doc, the `Runtime` / `Sync` / `Advanced` / `Needs` / `Hidden` flags,
tests, both example configs, a key no section fits) are in [CLAUDE.md](../CLAUDE.md) and [settings.md](settings.md). Code reads a value
through `a.cfg()` at the point of use; a runtime key copied into a variable at start is a bug.

**A board default the owner may override** (a threshold, a calibration, a device path). The
board's value is a profile key ([porting-a-speaker.md](porting-a-speaker.md)); the owner's is a registry key
next to it, in one of two forms:

- a hardware value read once at start: the key wrapped in `overrideV(...)` in `registry.go`
  (it shows empty unless the file sets it) and applied over the profile in `resolveBoard`
  (`internal/app/board.go`) when `cfg.Has(key)`; a restart key, `Advanced`. Examples:
  `mic_channels`, `wake_model`;
- a value read at the point of use (through `a.cfg()`, so it may be `Runtime`): a key whose zero
  means "the board's", resolved by a small helper (`splOffset` in `roomsound.go` for
  `mic_spl_offset`; also `auto_brightness_min`, `preroll_ms`), listed in `zeroIsBoard`
  (`internal/app/settings.go`). Its `Doc` says "0 uses the board's".

Both forms add the board's value to `boardValues` (`internal/app/settings.go`), so the settings
row shows what the board would use.

Neither is `Sync` when the value is this unit's (a calibration); a preference over a board
default (a temperature to warn at) may be.

**An HA option** (a value HA and the web app both change often, e.g. `follow_up`, `auto_brightness`,
`voice_ignore_guests`) is not a config key but persisted state:

1. a field in `persistState` (`internal/app/state.go`), with its default;
2. the entity in `mqtt/discovery.go` (`entity_category: config`; behind a builder flag when the
   board or a setting decides whether it exists);
3. a command handler in `commands()` (`internal/app/commands.go`) that updates the state and
   publishes it, and the state published in `publishAllState`;
4. its name in `syncOptions` (`sync.go`) if every speaker may share it;
5. an entry in `OPTIONS` (`web/src/settings.tsx`: its group and paragraph), and a config key that
   belongs right under it goes in `AFTER`.

## Sounds, animations and the rest: moments

A **moment** is this project's own term: a named event in the speaker's life (`listening`,
`voice_error`, `call_incoming`, `voice_guest`, …) to which the speaker reacts with something seen
or heard. Every such reaction (a picture or an animation on the front display, a sound, a phrase,
a ring effect, or a sequence of these) goes through moments. Code never draws a picture or plays a
sound in reaction to an event: it fires the moment by name, and what the moment shows comes from
the theme stack (the owner's packs, installed packs, the board,
the built-in default). So the owner can change it, a pack can restyle it, and boards with different
displays each get something that fits. Design: [display-expressions.md](display-expressions.md)
("Moments"), [asset-packs.md](asset-packs.md).

- A new moment is an entry in `momentCatalog` (`internal/app/defaulttheme.go`: name, when it fires,
  its slot, priorities, TTL) and its default in `defaultTheme` there; per display kind
  (seg7, pixels) in `momentDefaults` (`moments.go`) when the kinds need different pictures.
  `defaulttheme_test.go` checks the two lists agree.
- Fire it with `a.hook("name")` (its catalogue slot and priorities) or `a.moment(slot, "name",
  ttl)`; a held one (its default is `held(…)`, shown until the code ends it) is ended with
  `a.endHook("name")` (its layers, its steps and its sound), one fired with `a.moment` with
  `a.unexpress(slot)`. Not with
  `express()`, a sound player call or a ring effect of your own.
- What it shows: an expression, `anim:`, `sound:`, `phrase:`, `ring:`, `scenario:`, or steps of
  these (`{"steps": [...]}`); `a,b` picks one at random each time (a sound `a|b`: the first this speaker has). Prefer an existing expression
  or stock animation; a new picture ships as a default only if every display kind has one.
- **Rings and sounds a default can use**, which boards have which stock sound, and when a new
  moment may make a sound by default: [asset-packs.md](asset-packs.md), "What a default can use".
  In short: seen, not heard, unless the event must be heard; a sound one board lacks gets a
  fallback (`a|b`).
- Add it to the moments table of [asset-packs.md](asset-packs.md) (and
  [display-expressions.md](display-expressions.md) when it has per-kind pictures).
- What code still draws itself is a **reading**, not a reaction: a volume level, a countdown, a
  call's duration, a track title.

## HA entities and the web app: one definition

An entity is declared once in `mqtt/discovery.go`; HA gets it by discovery, and the web app shows
the same entity from the API's mirror of the speaker's state (`statemirror.go`): publish through
`a.publish` / `a.publishEvent`, never straight to the client, so the web app and a speaker without
a broker see it too. Topics come from `Topics` (`mqtt/topics.go`); a topic shared by every speaker
(arbitration, voice events, shared settings) is a method there too. An entity that is the speaker's
own setting and clutters HA's device card goes in `WebOnly`.

## The web app

- **The speaker card's chips** (health, voice pipeline, mic off, power problem) are listed by
  hand in `app.tsx` (`chips`): a new problem `binary_sensor` reaches HA, the controls tab and
  `/metrics` by itself, but not the card; add a chip next to `power_problem`'s.
- A new page is a `Tab` in `GROUPS` / `TAB_NAMES` (`app.tsx`), shown when the speaker reports the
  feature (`features()` in `internal/app/webextra.go`).
- **Which widget for what** (a switch, not a checkbox; counts as badges; reordering; lists) is
  in [design.md](design.md#components-the-web-app), with the palette and copy rules.
- **Reuse the widgets.** A setting or option shown outside its settings page goes through
  `SettingRows` (`settings.tsx`): the same switch, number, select, share switch and paragraph as on
  the page (the People tab's Guests card is the example). Entities use `Control` (`controls.tsx`).
  Don't build a second checkbox for something the page shows as a switch.
- **The front display** is painted by `paintFront` (`web/src/ledframe.ts`) everywhere: the LED page
  under its glass, a pack's previews plain. Frames come in the LED preview stream's terms (drive
  values, hex; `internal/frametap`).
- No `alert` / `confirm` / `prompt`: confirmations are built into the page ("Really …? Yes / No").
- Writes from a control that moves (a slider) save once it rests, not on every step: a shared
  value goes over MQTT on every save.
- Build with `tools/build.sh web` (no node needed on the host; `web/README.md`'s `pnpm dev`
  against a real speaker is a convenience for those who have it); check it in a browser against a speaker
  or the simulator ([led-preview.md](led-preview.md): `ledsim -http`).

## The API

`/api/<area>[/…]` in `apiX` handlers registered in `webserver.go`, behind `a.authed`. Answer with
`writeJSON` / `writeErr` (`{error}` with a 4xx); return empty lists as `[]`, not `null`. Document
the calls in the area's doc.

## Vendor code and child processes

Anything that links the vendor's libraries runs in a **child process** (`spotter-host` and its modes:
`serve` for the wake word, `voice` for voiceprints), supervised by the daemon (restart with
backoff, stdin/stdout line protocol): a crash there never takes the daemon or another feature
down. The daemon itself stays static Go. Read the vendor library's code before calling it (the
TFLite C API, for one, works only through the vendor's own op resolver: [voice-id.md](voice-id.md)).
Never run the vendor's stack to see what it does.

## Data on the speaker

Files under `<cfw>` (`a.cfwFile(name)`), written atomically (`.tmp` + rename), small and bounded
(a cap, the oldest dropped). List every new file in [deploying.md](guides/deploying.md) ("What ends up on
the device"); personal or biometric data gets 0600 and a note on where it lives.

## Tests

- Logic in its own package gets plain unit tests; `internal/app` has fixtures to start from:
  `newExpressApp` (display stacks), `themeTestApp`, `packTestApp` / `pixelPackApp`, `newWebApp`
  (the API over HTTP with a login), `fakeBroker` in `sync_test.go` (retained topics, several
  speakers).
- A child process is tested against a shell stand-in (`voiceid/host_test.go`,
  `app/voiceid_test.go`, `wake/callmode_test.go`).
- A behaviour moved behind a moment gets a pin in `regress_test.go`.
- No fixed sleeps for things that finish: poll with a deadline; listen on `127.0.0.1:0`.
- `go test ./...` in `yastationd/` (the Go container: `tools/build.sh yastationd` runs it).

## Examples to copy

| Pattern | Where |
|---|---|
| A watched condition → a diagnostic `binary_sensor` and a one-shot moment, once per boot | `internal/app/power.go` (`powerLoop`, `firstThisBoot`) |
| A held state moment, started on a change and ended on the way back, with a grace period | `internal/app/lifecycle.go` (`ha_lost` / `ha_back`, `a.endHook`) |
| An HA option end to end (state, entity, command, sync, web) | `voice_ignore_guests` (`state.go`, `discovery.go`, `voiceid.go`, `sync.go`, `settings.tsx`) |
| A child process with a line protocol, supervised | `internal/voiceid/host.go` (and its shell stand-in test) |
| A config key with a page section of its own | the `voice_id` group in `registry.go` |
| A web tab that reuses the settings rows | `web/src/voices.tsx` (the Guests card) |
| A board default the owner may override | `mic_spl_offset` (`splOffset`); `mic_channels` (`overrideV`, `resolveBoard`) |
| Self-monitoring: what is watched and the patterns | [health.md](health.md) |

## Before you call it done

The "Changes that ripple" list in [CLAUDE.md](../CLAUDE.md) (docs, deploy, config examples, build steps, the
README, the vendor repo), then build and deploy to a dev unit and check it there; sound only on the
unit the owner allows. The commit message says what a user gets: the changelog is written from
it at release. A release walks [releasing.md](releasing.md).
