# The web UI: one server per speaker, one app for all of them

**Status:** designed 2026-10-26; phases 1 (server, login, API, metrics, discovery, the setup page
under the same server) and 2 (the config editor's API with the trial; tested on Station 3 with
a config that broke Wyoming — rolled back in 3 s — and a good one — accepted) built the same day; phase 3 (the app: speakers, overview, controls, config
editor, login) and phase 4 (the LED preview, Wi-Fi, moments, About with an update check) the
same evening.

Every speaker runs one HTTP server in `yastationd`. It serves a static front-end app (built from
`web/`, embedded in the binary), a JSON API behind a login, Prometheus metrics, and — moved here —
the frame tap. The app finds the other speakers on the LAN and manages all of them from whichever
speaker you opened, so nobody has to remember IPs or dig them out of HA.

## Resources

Measured 2026-10-26: `yastationd` is 10–13 MB RSS on every board (the 256 MB Mini 3 has 153 MB
free); Go's `net/http` is already in the binary (the frame tap, the setup portal). An idle server
is one goroutine and ~100 KB; a request a goroutine with a few KB of stack; a Prometheus scrape
every 15 s, ~100 lines, is under 0.1 % of a Mini's CPU. The front-end bundle adds a few hundred KB
to the binary. Flash is written only when the config is saved.

## Server

- `http_addr` (default `:80`). One server: in Wi-Fi setup mode, requests that arrive on the access
  point's address get the setup page (a reduced app: the saved networks only), everything else as
  usual.
- Paths: `/` the app; `/api/…` JSON; `/metrics` Prometheus text; `/tap/…` the frame tap.

## Login

- `web_user` (default `admin`) and `web_password` — a PBKDF2-SHA256 hash
  (`pbkdf2-sha256$<iterations>$<salt b64>$<hash b64>`); without one the password is **`yastation`**.
  The default is written and documented on purpose: a speaker starts its life with open root adb
  anyway; hardening is the owner's call. Both can be changed from the app, which writes them into
  `yastationd.conf`.
- `POST /api/login {user, password}` → a bearer token, valid 30 days. Every `/api` call but
  `/api/info` and `/api/login` needs `Authorization: Bearer <token>`. Tokens in a header, not
  cookies: the app talks to other speakers cross-origin, CORS is open, and there is no cookie to
  forge requests with.
- A token is signed, not stored: its expiry and a nonce with an HMAC-SHA256 of them, keyed by
  the speaker's secret (`<cfw>/web.secret`, 32 random bytes written on first start) and the login
  (user and password hash). So a login survives restarts, redeploys and config saves; changing the
  password re-keys and logs everyone else out, and deleting `web.secret` logs everyone out. Until
  v0.16.8 tokens lived in memory and every restart logged everyone out.
- Failed logins are slowed down (a pause per failure per client).
- `/metrics` needs no login (read-only, the exporter convention); the frame tap needs one (it
  drives the displays).

## API

| Path | What |
|---|---|
| `GET /api/info` | id, name, board, model, version, the app's URL — no login (discovery) |
| `POST /api/login` | a token |
| `GET /api/peers` | the speakers this one knows (below) |
| `GET /api/state` | every web-exposed control and sensor: its metadata and current value |
| `POST /api/control/<object>` | set a control: the body is the same payload as its MQTT command |
| `GET /api/config`, `PUT /api/config` | the config file's text; a save is validated, then applied live or by a trial restart (below, and [settings.md](settings.md)) |
| `GET /api/assets`, `PUT /api/assets/videos/<name>/frames\|audio`, `POST …/use`, `DELETE /api/assets/videos/<name>` | the owner's media, by kind ([assets.md](assets.md)) |
| `/api/packs…` | asset packs and the theme stack, the import, the previews, the owner's own packs and their sounds and animations; the calls are listed in [asset-packs.md](asset-packs.md) ("Phase 3") |
| `GET /api/diag[?raw=1]` | the diagnostics archive (tar.gz) from `<cfw>/bin/diag.sh`, redacted unless raw ([diagnostics.md](guides/diagnostics.md)) |
| `GET /api/vitals` | the speaker card's vitals: the speaker's and the daemon's uptime, load and cores, memory (total, available, the daemon's), the flash under the CFW, every thermal zone |
| `GET /api/settings`, `PUT /api/settings` | the config keys with their descriptions; set some ([settings.md](settings.md)) |
| `GET /api/sync`, `POST /api/sync` | what is shared with the other speakers; share or stop sharing an item |
| `POST /api/password` | change the login |
| `GET /api/wifi`, `POST /api/wifi/networks` | the saved Wi-Fi networks (no passwords), the current one, whether setup mode is on; add a network (`{ssid, psk, hidden, priority}`) — only with `wifi_setup` on ([wifi-setup.md](wifi-setup.md)) |
| `GET /api/moments[?pack=]`, `PUT\|DELETE /api/moments/<name>[?pack=]`, `POST /api/moment/<name>` | the moments, the layer each comes from, the owner's packs and the catalogue; set or remove a moment in one of the owner's packs; show one (a body of candidates previews an unsaved draft) |
| `GET /api/log?source=daemon\|system&lines=N` | the tail of the daemon's or the system log, oldest first ([storage.md](storage.md)) |
| `GET /api/voices`, `GET /api/voices/log\|preview`, `POST /api/voices/groups/<id>\|merge\|entries/<id>`, `DELETE /api/voices[/groups/<id>]` | voice ID: where the store is; on the store the groups, the log, the threshold preview and the owner's edits ([voice-id.md](voice-id.md)) |
| `GET\|POST\|DELETE /api/calibrate/light` | the light-sensor calibration: its state; `{"lux": N}` measures and stores a point; drop the unit's own ([sensor-calibration.md](sensor-calibration.md)) |
| `GET\|POST\|DELETE /api/calibrate/acoustic` | Station 3 only: room/response measurement. `GET` observes a run; `{"level_dbfs": -50\|-45\|-40}` starts three ~30 s opt-in sweeps with voice paused; `DELETE` cancels. Report only — it never alters playback sound ([acoustic-calibration.md](acoustic-calibration.md)) |

### One definition for MQTT and the web

The controls HA sees are already defined once: the discovery entities (`mqtt.Builder.Entities()`:
name, kind, options, ranges, units), the command handlers (`app.commands()`), and the state the app
publishes. The web uses the same three, nothing is described twice:

- **State:** every state publish goes through one call (`a.publishState(object, value)`) that
  stores the last value of each object and then publishes it. `/api/state` returns the stored
  values next to the entities' metadata — even with no broker configured.
- **Commands:** `/api/control/<object>` calls the same handler as the MQTT command topic, with the
  same payload.
- **Which ones:** an entity is on the web unless it is a light (the ring and the ambilight stay
  HA's) — runtime settings (brightness, auto-brightness, display mode, expression, follow-up,
  durations), sensors (light, sound, supply, health, version) and buttons.

### Saving the config

1. The new text is parsed with the daemon's own parser; an error goes back to the app, nothing is
   written.
2. The current file is kept as `yastationd.conf.bak` and the new one written atomically. When
   every changed key is a runtime one it applies at once and that is all ([settings.md](settings.md));
   otherwise a trial marker `<cfw>/config.trial` is left, the daemon exits and the supervisor starts
   it again.
3. The marker holds the speaker's health before the save. The new config is accepted after a
   minute only if the speaker is at least as healthy (broker, HA's pipeline); a server that cannot
   listen (the web, Wyoming) fails it at once. A failing trial puts the `.bak` back, removes the
   marker and restarts; a daemon that crashes on trial gets the same from the supervisor. A bad
   config costs a restart, never the speaker. (Found on glass: `http_addr = :10700` passed the
   parser, the web took Wyoming's port and HA lost the satellite — hence health, not just "up".)
4. Wi-Fi: networks can only be added from a running speaker; removing or changing one stays in the
   setup mode (the access point), which is the safe place to lose the client connection.

## Metrics

`GET /metrics` (no login), Prometheus text, every series labelled `device`:
`yastation_info{board,version}`, `yastation_uptime_seconds`, the process (`yastation_rss_bytes`,
`yastation_go_heap_bytes`, `yastation_goroutines`), the system (`yastation_memory_bytes{kind=total|
available|free}`, `yastation_load{span=1m|5m|15m}`, `yastation_temperature_celsius{zone}` per
thermal zone — `soc_thermal` on every board so far, `yastation_system_uptime_seconds`), and every numeric or on/off state the app
publishes (`yastation_lux`, `yastation_sound_floor`, `yastation_volume`, `yastation_mic_mute`…;
`yastation_health{state}`).

## Discovery

Each speaker publishes a retained `yastation/<id>/web/info` (`{id, name, board, version, url}`)
and subscribes to `yastation/+/web/info`; `/api/peers` lists what it has heard (and itself). The
app asks the speaker it was loaded from, then talks to each peer at its own URL. The broker
already connects them all; mDNS (`<id>.local`) may come later.

## The app (`web/`)

A subproject: TypeScript, Preact, Vite (versions pinned exactly), built into
`yastationd/internal/webui/dist/app/` and embedded (`go:embed`). The build output is not committed:
`make build` rebuilds it whenever `web/` changed, in the pinned builder container
(`tools/build.sh web`, `docker/node.Dockerfile`), so the host needs docker, not Node. A plain
`go build` without it still compiles; that binary serves a "built without its web app" page.

- The speakers list (from `/api/peers`): name, room, board, version, online, a login state.
- Per speaker: a card on top (name, address, status, volume, mute, display), sensors, runtime
  controls (from `/api/state`, rendered by kind), the configuration pages
  ([settings.md](settings.md)), the password, the frame tap's preview.
- Logins: one per speaker; the app tries the last credentials that worked on the others first.

## Navigation

Per speaker, a section menu beside the content: the groups as captions, their sections under
them (a scrolling row on narrow screens). The open section is in the address (`#options`), so it
can be linked (the old `#options`, `#settings` and `#config` open General). Lists of settings and controls are settings pages — one narrow column, a name and
its control on one line — while sensors, the log, the LED preview and the config file use the
window's width.

| Group | Tabs |
|---|---|
| Status | Sensors (and diagnostics), Log |
| Controls | Display and voice (the runtime controls HA has: lights, display, expressions, volume…), LED |
| Behaviour | Moments, Packs, Sounds, Videos — what the speaker does when something happens ([asset-packs.md](asset-packs.md)); a short explanation, folded once read, sits above each |
| Configuration | General, Voice, People (with voice ID on: the album of voices on the store speaker, a pointer to it on the others; [voice-id.md](voice-id.md)), Home integration, Music Assistant, Intercom, Calibration, Wi-Fi, Login — [settings.md](settings.md) |

## Calibration

The light sensor against a lux meter, the web app's version of
[sensor-calibration.md](sensor-calibration.md#from-the-web-app): the user measures the light next
to the speaker's top in a well-lit room and enters the lux; `POST /api/calibrate/light {"lux": N}`
(behind the login) blacks out the front display and the ring, takes the median of 30 readings
over 15 s, and stores the pair as `lux_calibration` in the speaker's config (two ways: an incident
light meter — lux, or EV / an exposure converted with C = 250 — or a phone or camera metering a
sheet of white paper on the speaker's top in reflected light, converted with K = 12.5 and the
paper's 85 % reflectance; the page converts as it is typed) — live, no restart
(`GET` shows the calibration in use, `DELETE` drops the unit's own). Under 30 lx it refuses.

## The extra tabs

- **LED preview** (`led_preview`, on by default): the frame tap under `/tap/` on the web server,
  behind the login (the token also as `?token=`, since an EventSource sends no headers); the tab
  draws the screen (or the minis' 7-segment clock), the ring and the ambilight like the old viewer;
  the front display as on the glass, its light diffused and seen through the fabric's weave. Nothing is encoded while no
  one watches. `frametap_addr` still opens the tap's own listener without a login (the host
  simulator).
  Under the preview the speaker's animations in a column view, as a file browser's: the top
  folders ("In force": the firmware's, with its own subfolders, and the worn theme's; one per
  installed pack, by its id), each opened folder in a column to the right, and a click on an
  animation plays it, a pack's without applying the pack (`@<pack>/<name>`); the arrow keys walk
  the columns, a search lists matches across all of them; `#led/<folder>` opens a folder
  ([asset-packs.md](asset-packs.md), phase 3).
- **Wi-Fi:** the saved networks (no passwords), the one connected now (`wpa_cli status`), and
  adding a network — never changing or removing one from a running speaker. The file is written
  (the old one as `.before-web`), wpa_supplicant is not reloaded: the new network is used at the
  next reconnect or reboot, so the connection in use is never touched. The rest is the setup
  mode's.
- **Moments:** every moment, what it shows here and the layer that comes from (a pack, or
  built-in), a Play button, and an editor working in one of the owner's packs ("Edit in"):
  Customize copies what the moment does now into that pack; per moment a list of candidates (the
  first this speaker has plays), each an expression, an animation of its front display (typed to
  filter: Station 3 has about a hundred), a scenario, a sound, a ring effect or a sequence of
  steps (expression, animation, ring effect, text, sound; a length or the animation's own; the
  last one may hold). "Play draft" shows it unsaved, Save keeps it in the pack, "Remove from" lets
  the layers below show again ([asset-packs.md](asset-packs.md), [display-expressions.md](display-expressions.md),
  "The owner's moments").
- **Log:** the tail of the daemon's log (`/tmp/yastationd.log`) or of the system log
  (`/var/log/messages` with `messages.0`: the kernel, the Wi-Fi driver, dropbear), 100–2000 lines,
  a filter, warnings and errors coloured; while "follow" is on it refreshes every 3 s and stays at
  the bottom. `GET /api/log?source=daemon|system&lines=N`, behind the login. What the speaker has
  now: both live in RAM, so a reboot empties them (the log server keeps the history:
  [storage.md](storage.md)).
- **About:** the repository, the changelog, and an update check: the browser asks GitHub's API
  for the newest `vX.Y.Z` tag (it allows any origin; the speakers never go online), links its
  GitHub Release, and marks speakers whose version is older. The answer is kept for the tab for
  10 minutes, and asked again at once when a speaker reports a newer version than it. No internet
  in the browser, no check.

## Phases

1. Server, login, `/api/info|state|control|peers`, `/metrics`, the state mirror, discovery; the
   setup portal under the same server.
2. The config editor with the trial and rollback (supervisor change).
3. The app: speakers, dashboard, config, password.
4. The frame tap under the server and the app; the setup-mode page in the app's style.
