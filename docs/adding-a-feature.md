# Adding a feature (agent guide)

How the CFW is built and the conventions a new feature follows, so it fits next to the rest
instead of beside it. For a new speaker model, read [porting-a-speaker.md](porting-a-speaker.md)
instead; for what a change must touch outside the code (docs, deploy, config examples, the vendor
repo), the "Changes that ripple" list in `CLAUDE.md`.

**The short version.** A setting is a key in the registry. Anything the speaker does in reaction to
an event (a picture, a sound, a ring effect) is a **moment**. An HA entity and its web app control
are one definition. The web app reuses the settings page's widgets and the LED page's painter. A
vendor library runs in a child process. Read the doc of the area first (`docs/index.md`), and say
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
| `web/src/` | the web app (Preact): `app.tsx` (tabs), `settings.tsx`, `controls.tsx`, `moments.tsx`, `packs.tsx`, `extra.tsx` (LED, log), `ledframe.ts`, `api.ts` |

## Settings: a key in the registry

Every config key is declared **once**, in `internal/config/registry.go`; that one entry gives the
parser, the settings page row (label, control, paragraph), the API and the sync between speakers.
The rules (group, label, doc, the `Runtime` / `Sync` / `Advanced` / `Needs` / `Hidden` flags,
tests, both example configs) are in `CLAUDE.md` and [settings.md](settings.md). Code reads a value
through `a.cfg()` at the point of use; a runtime key copied into a variable at start is a bug.

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

## Reactions: moments

Code never draws a picture or plays a sound in reaction to an event: it fires a **moment**, and
what the moment shows comes from the theme stack (the owner's packs, installed packs, the board,
the built-in default). So the owner can change it, a pack can restyle it, and boards with different
displays each get something that fits. Design: [display-expressions.md](display-expressions.md)
("Moments"), [asset-packs.md](asset-packs.md).

- A new moment is an entry in `momentCatalog` (`internal/app/defaulttheme.go`: name, when it fires,
  its slot, priorities, TTL) and its default in `defaultTheme` there; per display kind
  (seg7, pixels) in `momentDefaults` (`moments.go`) when the kinds need different pictures.
  `defaulttheme_test.go` checks the two lists agree.
- Fire it with `a.hook("name")` (its catalogue slot and priorities) or `a.moment(slot, "name",
  ttl)`; end a held one with `a.unexpress(slot)`. Not with `express()`, a sound player call or a
  ring effect of your own.
- What it shows: an expression, `anim:`, `sound:`, `phrase:`, `ring:`, `scenario:`, or steps of
  these (`{"steps": [...]}`); `a,b` picks one at random each time (a sound `a|b`: the first this speaker has). Prefer an existing expression
  or stock animation; a new picture ships as a default only if every display kind has one.
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

- A new page is a `Tab` in `GROUPS` / `TAB_NAMES` (`app.tsx`), shown when the speaker reports the
  feature (`features()` in `internal/app/webextra.go`).
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
- Build with `tools/build.sh web` (never node on the host); check it in a browser against a speaker
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
(a cap, the oldest dropped). List every new file in [deploying.md](deploying.md) ("What ends up on
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

## Before you call it done

The "Changes that ripple" list in `CLAUDE.md` (docs, deploy, config examples, build steps, the
README, the vendor repo), then build and deploy to a dev unit and check it there; sound only on the
unit the owner allows. A release walks [releasing.md](releasing.md).
