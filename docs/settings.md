# Settings: one registry, live reload, sharing between speakers

Status (2026-10-28): implemented; tested on the desk (Go tests, the simulator's web app).

## The registry

Every config key is described once, in `yastationd/internal/config/registry.go` (`config.Keys`):
its parser and printer, a group, a label, an explaining paragraph, and flags:

| Flag | Meaning |
|---|---|
| `Runtime` | a change applies while the daemon runs; otherwise saving it restarts the daemon on trial |
| `Sync` | the key may be shared by every speaker; never set for a speaker's own (identity, addresses, calibrations, files, ports that must differ) |
| `Hidden` | set elsewhere in the app (the login, the light calibration) or only by tests and the daemon (`sysroot`, `state_file`, `heartbeat_file`): not on the pages |
| `Advanced` | rarely touched (ports, paths, timing knobs, first-start seeds, hardware overrides): folded under "Advanced" on its page |
| `Needs` | the hardware a key is for (`ambilight`, `pixels` = a dot-matrix front screen): boards without it don't list the key |

The parser (`config.Parse`) goes through the same table, so a key cannot be parsed and described
differently. Hardware overrides print empty unless the file sets them (`Config.Has`): the board
profile's value rules then.

**First-start seeds.** `clock_brightness`, `auto_brightness`, `ring_brightness` and
`ambilight_brightness` only give a new speaker its first values; the live ones are HA's
entities, kept in the state file (`yastationd.state`). They stay there: HA and automations change
them often, and the state file is written debounced, not the config with its comments.

## Live reload

The config file stays the one place for settings: the settings pages, the config editor, the login,
the light calibration and the sync all rewrite it (`config.EditNote`, comments kept, the old text as
`.bak`). `App.applyConfigText` then compares the new config with the one in force
(`config.Changed`, which also counts a key the file starts or stops setting):

- **only runtime keys changed:** the new config is swapped in (`App.conf`, an atomic pointer; code
  reads it through `a.cfg()` at the point of use, so the next read sees the new value);
- **any other key changed:** the trial restart of [web-ui.md](web-ui.md) ("Saving the config"):
  written with a trial marker, the daemon restarts, a worse-off speaker rolls back by itself;
- **comments only:** written, nothing applied.

A runtime key must be read where it is used, never copied into a long-lived variable at start (the
light loop's curve and the music ducking level were such copies; they read the config each tick
now). Keys whose consumers are built once at start (the front display's text lines, the time zone,
the intercom engine, child processes) are restart keys.

Writers that apply their own key (the login, the light calibration) call `adoptConfig` after
writing, so a later diff doesn't mistake their keys for changes needing a restart.

## The pages (`web/src/settings.tsx`)

The Configuration group, by topic rather than by mechanism. Which section is on which page lives
in the registry (`config.Groups`, `Page`), so a new key only picks a section:

| Tab | Sections |
|---|---|
| General | Display (with HA's display options), Theme (the packs in force), Ambilight, Sound, Video, a new speaker's first values |
| Voice | the wake word, the dialog with HA (follow-up, the reply deadline, a wake word while waiting), which speaker answers, voice ID (who is talking, ignoring guests; [voice-id.md](voice-id.md)), the voice front-end's hardware |
| Moments | what the speaker shows for each event (display-expressions.md) |
| Home integration | Identity (name, ID, board), Home Assistant (MQTT, room sound sensors, Wyoming, health probes), Web app, Time, Logs. Usually set once |
| Music Assistant | the Sendspin player and the music on the display |
| Intercom | calls between speakers |
| Calibration | the light sensor (its own tool), the mic's dB SPL offset, and (Station 3) the acoustic room measurement ([acoustic-calibration.md](acoustic-calibration.md)) |
| Wi-Fi | the saved networks, the setup mode's settings, and what happens when the connection is lost (the reboot watchdog, Wi-Fi recovery). Always there, so the setup mode can be turned on from it |
| Login | the web login |

**A key no section fits.** Put it in the section an owner would look in for that topic, not the
one whose mechanism it shares (a temperature warning is about the speaker's health, not the
Wi-Fi page's "when the connection is lost"). When none is about its topic, add a section to
`config.Groups` on the page it belongs to and say why in the commit; a whole new feature may get
its own page. Never a section or page per mechanism ("MQTT", "Timers").

There is no Advanced page: on every page the keys marked `Advanced` (rarely touched: ports, paths,
timing knobs, first-start values, overrides of the board's hardware profile) are folded under
"Advanced" at the bottom, next to what they belong to. Keys only tests or the daemon itself use
(`sysroot`, `state_file`, `heartbeat_file`) and the ones set on their own tabs (the login, the
light calibration) are `Hidden`.

- Every page mixes keys that apply at once with keys that need a restart. The first are saved as
  they change; the second carry a "restart" tag and their changes wait in a floating bar for one
  "Apply and restart" (on trial).
- Each row: a name, a control, a share switch, and the paragraph under it.
- **Changed settings stand out:** a key the file sets to something other than its value when
  unset — the default, or the board profile's for the hardware overrides, the mic calibration and
  the brightness curve (`board` in the API) — is marked and has a Reset (back to unset).
- **A bad line in the file never keeps the speaker from starting:** the daemon loads it leniently
  (`config.ParseLenient`): a line this build refuses (a value an older build took, an unknown
  key) is logged and skipped, its key keeps the default, and the pages show it. Writers refuse a
  new bad line but let an old one stay, so it never blocks other changes; setting the key replaces
  it.
- Secrets (`mqtt_pass`, `intercom_secret`, `wifi_setup_psk`) are never sent back: a mask stands for
  a set one, and the mask sent back leaves it unchanged.
- Every key is on a page, so the app has no raw-file editor any more; `GET`/`PUT /api/config`
  stays for scripts and takes the same path on save (runtime changes live, others restart).
- **The speaker card** above every tab: the name, the address, the model and version and status
  chips (health, HA's voice pipeline, mic muted, power, light); beside them a panel of vitals
  (`GET /api/vitals`, every 5 s): the speaker's and the daemon's uptime apart, load, the hottest
  thermal zone (all of them on hover), memory used, the daemon's memory, flash used, each with a
  thin gauge (load over the cores, 30–90 °C, shares of the total), amber or red when it runs short
  or hot; under both a toolbar of the everyday controls (volume with
  mute, display mode, faces). Mute sets the volume to 0; the level before is kept in the browser
  to come back to.
- **Options and keys side by side:** a config key may sit right after the HA option it belongs to
  (`AFTER` in settings.tsx): "Auto brightness: dimmest" follows "Clock brightness", its brightest.

API: `GET /api/settings` (groups and keys with values, defaults, flags), `PUT /api/settings`
`{"changes": {key: [lines]}}` (`[]` removes a key) → `{restarting, changed}`.

## Sync

An item the owner shares lives in one retained topic, `<prefix>/shared/<kind>/<item>`, payload
`{"v": value, "at": unix ms, "by": device id}`; an empty payload means not shared. Kinds:

| Kind | Item | Value |
|---|---|---|
| `setting` | a config key with `Sync` | its lines, e.g. `["2"]`; `null` while it is at the default |
| `option` | an HA option entity (clock brightness, auto brightness, durations, follow-up, faces, ambilight fx brightness, ignore guests) | its state, e.g. `"40"` |

- **Sharing** an item publishes this speaker's value; every speaker takes it (a config key through
  `applyConfigText`, so a restart key restarts each speaker on trial; an option through its command
  handler). **Unsharing** clears the topic; every speaker keeps the
  value it has.
- **Defaults stay defaults.** A setting the owner left at the default (the file doesn't set it, or
  sets the default value) is shared as `null`, and a speaker taking `null` removes the key from
  its file: each runs its own build's default, so a default changed in a new version reaches every
  speaker. A shared value equal to this build's default counts as `null` too (a value shared before
  the default changed, e.g. `arbitration_wait_ms = 700`, no longer pins it). Only a real override
  travels as a value.
- **A local change** of a shared item (any writer: the pages, HA, the config editor, a restart key
  on its way down) goes out with the new value. A speaker that already holds the shared value
  publishes nothing, and a value taken from the others never goes out again: that is what stops
  loops. Edits of the file are read and written under one lock, since two taken at once must not
  undo each other (v0.24.0 and before: two shared keys changing together sent all speakers into an
  endless exchange, rewriting the config many times a second).
- **The fuse.** Speakers trading an item without end rewrite the config on flash, so a speaker stops
  the sync by itself when one item changes hands too often, whatever the cause: more than 10 changes
  in a minute whose author differs from the change before (A, B, A, B), or more than 120 changes in
  a minute by anyone. The first is the sign of a storm; a slider dragged on one speaker has one
  author and doesn't count there, the ceiling only catches a speaker looping by itself. For 10
  minutes (doubling on each repeat, up to an hour; two quiet hours start again from 10) it takes and
  sends no shared item, logs `sync: speakers keep changing a shared item`, and `GET /api/sync` shows
  `paused_until`. Local changes in that time stay local; at the end it takes the latest shared
  values, so a local change of a shared item is overwritten. Unsharing still works.
- **Two changes at once:** every speaker takes the messages in the broker's order and ends on the
  last one. No timestamps decide, no leader is needed.
- **Offline speakers** get the retained values when they connect.
- **A new speaker** (once it has the broker in its config) gets every shared item on its first
  connect and takes them: runtime keys and options at once, and the restart keys written together with one restart on trial. Its `.bak` and the
  trial's health mark stay those from before the first restart key, so a rollback undoes them all.
  After the restart the same retained values change nothing. It never sends its own values for
  items already shared; its unshared settings and its own keys (name, ID, calibrations) stay.
- Options are checked together after any option change, in case a handler changes another one
  too.
- **A shared value whose trial fails here** is not taken again on the next connect, which would
  restart the speaker in a loop: `<cfw>/sync.pending` lists the shared values a running trial came
  from, the trial's acceptance removes it, and found at start with no trial running it moves to
  `<cfw>/sync.rejected`. Such an item shows as not taken until it changes again.
- Values cross the broker in the clear, secrets included (the intercom secret is a natural one to
  share). The broker is on the LAN; put a login on it.

API: `GET /api/sync` (what is shared, when, by whom; which options this speaker can share),
`POST /api/sync {kind: "setting"|"option", item, on}`. Moments are not shared since v0.22 (they
travel in packs, exported and installed; a retained `moment/<kind>` item from an older speaker is
ignored), and neither is the theme since v0.21.2. Without a broker sharing is off
(503).

## Tests

`internal/config/registry_test.go` (every key's default parses back; the special parsers; overrides;
`Changed`), `internal/app/settings_test.go` (apply: comments, invalid, runtime, mixed, overrides;
the login and calibration writers; the API's kinds, lists, secrets, flags), `internal/app/sync_test.go`
(an in-memory broker with retained topics and in-order delivery: two speakers converge, both change
at once, a late speaker, unsharing, a restart key, options pulling each other, moments not
shared, the API).

## Retired keys

A key a newer version drops is listed in `config.Retired` (registry.go): a config file that still
sets it loads as before, the line ignored, so an update never fails on an old config. The board
profiles do the same for their keys (`retiredKeys`, board.go). Retired so far: `display_faces`
(themes decide what the front shows); the profile keys `audio.volume_sound`,
`audio.announce_sound`, `audio.error_sound`, `power.alert_sound` (the sounds are moments).
