# Settings: one registry, live reload, sharing between speakers

Status (2026-10-28): implemented; tested on the desk (Go tests, the simulator's web app).

## The registry

Every config key is described once, in `yastationd/internal/config/registry.go` (`config.Keys`):
its parser and printer, a group, a label, an explaining paragraph, and flags:

| Flag | Meaning |
|---|---|
| `Runtime` | a change applies while the daemon runs; otherwise saving it restarts the daemon on trial |
| `Sync` | the key may be shared by every speaker; never set for a speaker's own (identity, addresses, calibrations, files, ports that must differ) |
| `Hidden` | set elsewhere in the app (the login, the light calibration): not on the settings pages |
| `Advanced` | paths, test hooks, hardware overrides, first-start seeds: under "Advanced" |
| `Needs` | the hardware a key is for (`ambilight`, `pixels` = a dot-matrix front screen): boards without it don't list the key |

The parser (`config.Parse`) goes through the same table, so a key cannot be parsed and described
differently. Hardware overrides print empty unless the file sets them (`Config.Has`): the board
profile's value rules then.

**First-start seeds.** `clock_brightness`, `auto_brightness`, `display_faces`, `ring_brightness`
and `ambilight_brightness` only give a new speaker its first values; the live ones are HA's
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

- **Settings:** HA's option entities and the runtime keys, by group; each row a name, a control, a
  share switch, and the paragraph under it (with the default when the value differs). A change is
  saved at once.
- **Config:** the restart keys, changed together and applied with one "Apply and restart"; the
  advanced keys folded away; "Edit the config file" opens the raw text (whose save takes the same
  path: runtime changes apply live, others restart).
- **Changed settings stand out:** a key the file sets to something other than its value when
  unset — the default, or the board profile's for the hardware overrides, the mic calibration and
  the brightness curve (`board` in the API) — is marked and has a Reset (back to unset).
- Secrets (`mqtt_pass`, `intercom_secret`, `wifi_setup_psk`) are never sent back: a mask stands for
  a set one, and the mask sent back leaves it unchanged.
- **Moments** sit with the settings and have a share switch per moment.
- **The speaker card** above every tab: the name, the address, the model and version, status chips
  (health, HA's voice pipeline, mic muted, power, light, uptime), and the everyday controls (volume
  with mute, display mode, faces). Mute sets the volume to 0; the level before is kept in the
  browser to come back to.

API: `GET /api/settings` (groups and keys with values, defaults, flags), `PUT /api/settings`
`{"changes": {key: [lines]}}` (`[]` removes a key) → `{restarting, changed}`.

## Sync

An item the owner shares lives in one retained topic, `<prefix>/shared/<kind>/<item>`, payload
`{"v": value, "at": unix ms, "by": device id}`; an empty payload means not shared. Kinds:

| Kind | Item | Value |
|---|---|---|
| `setting` | a config key with `Sync` | its lines, e.g. `["2"]` |
| `option` | an HA option entity (clock brightness, auto brightness, durations, follow-up, faces, ambilight fx brightness) | its state, e.g. `"40"` |
| `moment/<display kind>` | a moment, shared only by speakers whose front display is of that kind (`seg7`, `pixels`) | its candidates, `null` for the defaults |

- **Sharing** an item publishes this speaker's value; every speaker takes it (a config key through
  `applyConfigText`, so a restart key restarts each speaker on trial; an option through its command
  handler; a moment through `setMoment`). **Unsharing** clears the topic; every speaker keeps the
  value it has.
- **A local change** of a shared item (any writer: the pages, HA, the config editor, a restart key
  on its way down) goes out with the new value. A speaker that already holds the shared value
  publishes nothing, which is what stops loops.
- **Two changes at once:** every speaker takes the messages in the broker's order and ends on the
  last one. No timestamps decide, no leader is needed.
- **Offline speakers** get the retained values when they connect.
- **A new speaker** (once it has the broker in its config) gets every shared item on its first
  connect and takes them: runtime keys and options at once, moments if its display is of the same
  kind, and the restart keys written together with one restart on trial. Its `.bak` and the
  trial's health mark stay those from before the first restart key, so a rollback undoes them all.
  After the restart the same retained values change nothing. It never sends its own values for
  items already shared; its unshared settings and its own keys (name, ID, calibrations) stay.
- Options are checked together after any option change: a manual clock brightness turns auto
  brightness off, and both go out.
- Values cross the broker in the clear, secrets included (the intercom secret is a natural one to
  share). The broker is on the LAN; put a login on it.

API: `GET /api/sync` (what is shared, when, by whom; which options this speaker can share),
`POST /api/sync {kind: "setting"|"option"|"moment", item, on}`. Without a broker sharing is off
(503).

## Tests

`internal/config/registry_test.go` (every key's default parses back; the special parsers; overrides;
`Changed`), `internal/app/settings_test.go` (apply: comments, invalid, runtime, mixed, overrides;
the login and calibration writers; the API's kinds, lists, secrets, flags), `internal/app/sync_test.go`
(an in-memory broker with retained topics and in-order delivery: two speakers converge, both change
at once, a late speaker, unsharing, a restart key, options pulling each other, moments by display
kind, the API).
