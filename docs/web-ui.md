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
- `POST /api/login {user, password}` → a bearer token (random, kept in memory, valid 30 days,
  lost on a restart). Every `/api` call but `/api/info` and `/api/login` needs
  `Authorization: Bearer <token>`. Tokens in a header, not cookies: the app talks to other
  speakers cross-origin, CORS is open, and there is no cookie to forge requests with.
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
| `GET /api/config`, `PUT /api/config` | the config file's text; a save is validated, then applied (below) |
| `POST /api/password` | change the login |

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
2. The current file is kept as `yastationd.conf.bak`, the new one written atomically, and a trial
   marker `<cfw>/config.trial` left; the daemon exits and the supervisor starts it again.
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
thermal zone — `soc_thermal` on every board so far), and every numeric or on/off state the app
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
- Per speaker: sensors, runtime controls (from `/api/state`, rendered by kind), the config editor,
  the password, the frame tap's preview.
- Logins: one per speaker; the app tries the last credentials that worked on the others first.

## The extra tabs

- **LED preview** (`led_preview`, on by default): the frame tap under `/tap/` on the web server,
  behind the login (the token also as `?token=`, since an EventSource sends no headers); the tab
  draws the screen, the ring and the ambilight like the old viewer. Nothing is encoded while no
  one watches. `frametap_addr` still opens the tap's own listener without a login (the host
  simulator).
- **Wi-Fi:** the saved networks (no passwords), the one connected now (`wpa_cli status`), and
  adding a network — never changing or removing one from a running speaker. The file is written
  (the old one as `.before-web`), wpa_supplicant is not reloaded: the new network is used at the
  next reconnect or reboot, so the connection in use is never touched. The rest is the setup
  mode's.
- **Moments:** every moment this speaker maps (its display kind's defaults, the board's, the
  expressions), what each resolves to here, and a button that shows it (6 s at most).
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
