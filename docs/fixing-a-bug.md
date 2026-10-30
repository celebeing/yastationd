# Fixing a bug (agent guide)

How a bug report becomes a fix here: find the code path, reproduce it without risking a speaker,
pin it with a test, fix, deploy to a dev unit, record it. For a new feature read
[adding-a-feature.md](adding-a-feature.md); for a new model, [porting-a-speaker.md](porting-a-speaker.md).
The safety doctrine in [CLAUDE.md](../CLAUDE.md) applies throughout.

## 1. From the symptom to the code

- **The symptom table first:** [guides/troubleshooting.md](guides/troubleshooting.md) maps what an
  owner sees (no reaction to «Алиса», the ring, no sound, a call, after an update) to log lines and
  the spec that explains it. Many "bugs" are a setting or HA (a muted mic, `wake_enabled`, the
  Wyoming integration).
- **The spec of the area** from [index.md](index.md) (or the map, [architecture.md](architecture.md)), then
  the code it names. Paths that cross several areas:

  | Symptom about | Start at |
  |---|---|
  | the ring, the front display, a sound in reaction to something | the moment's name in the moments table ([asset-packs.md](asset-packs.md)); `momentCatalog` / `defaultTheme` (`internal/app/defaulttheme.go`); `fire` / `endHook` (`moments.go`); the steps (`sequence.go`); the layer stacks (`internal/display/stack.go`); the LED writes (`internal/hw/sysfs.go`) |
  | a key, the mute, the touch ring | [input.md](input.md) |
  | booting, degraded, HA lost | [status-indication.md](status-indication.md), [health.md](health.md) |
  | the wake word | [wake-word.md](wake-word.md) |
  | a request, a reply, a timeout | [voice-slow-replies.md](voice-slow-replies.md), `voice.go`, `satellite.go` |
  | a setting not applied | [settings.md](settings.md) (runtime vs restart keys) |
  | the web app | [web-ui.md](web-ui.md), `web/src/` ([adding-a-feature.md](adding-a-feature.md), "Where things are") |

- **The board** matters more often than not: the board doc's quirks
  ([boards/README.md](boards/README.md)), the profile (`internal/board/profiles/<codename>.conf`). The
  retail names: Station 3 = orion, Station Mini 3 = bergamot, Mini 3 Pro = pickle.

## 2. Reproduce, without risking a speaker

**Read-only first**, no approval needed beyond the task itself:

- the log (`/tmp/yastationd.log` or the web app; tmpfs, so before any reboot), the log server if
  the owner ships logs ([storage.md](storage.md));
- HA's entity history (the state as the speaker published it);
- what the daemon draws: the LED preview stream ([led-preview.md](led-preview.md)), apart from what
  the glass shows;
- the speaker's config, the versions (`yastationd -version`, the Version sensor), the diagnostics
  archive ([guides/diagnostics.md](guides/diagnostics.md): it also lists the commands **never** to run on a
  speaker).

**Ask the owner first** for anything that:

- makes sound, light or a call, or needs a physical control (the mute switch, a key, the touch
  ring): only they can press it, and only on the unit they name;
- changes a setting (`log_level = debug` included): note the value before, put exactly it back
  after;
- restarts the daemon or a helper, or reboots;
- touches Wi-Fi, the egress lock, the vendor gate or the rootfs: don't, as part of a bug fix.

**Reproduce on the host when you can:** the daemon's logic runs in tests with fake sysfs trees,
fake brokers and shell stand-ins for the helpers ([adding-a-feature.md](adding-a-feature.md), "Tests";
the fake sysfs trees: `internal/hw/hw_test.go` `TestRingFromPattern`, `power_test.go`);
the LED simulator (`cmd/ledsim`) shows the stacks without a speaker.

## 3. Pin it, fix it

- **A failing test first**, next to the closest existing one: `regress_test.go` for what a moment
  shows (`TestRegressMute` and friends), the area's `_test.go` otherwise. A sequence of events
  (on → off → on within milliseconds) is a few calls in a test, not a hand on a speaker.
- Fix the cause, not the symptom (a debounce hides a race; a forced redraw hides a lost write: say
  which it is in the commit).
- `tools/build.sh yastationd` runs `go test ./...` in the container. A failure that is on `main`
  already is not yours: check [backlog.md](backlog.md) ("Correctness") before chasing it, and
  don't paper over it.

## 4. Deploy and check

- `tools/deploy.sh <hosts-file>` with the dev units the owner keeps for testing (the owner's
  gitignored `ACCESS.local.md` lists them); [guides/deploying.md](guides/deploying.md) §4. Every unit must
  print `ok`; one that didn't still runs the old build.
- Deploy to one unit of each board the fix touches. A fix to shared code goes to the dev units
  first, to the rest after the owner looked.
- Check the symptom the way it was reported, with the owner when it needs them (above).

## 5. Record it

- **The docs** of the area, if the bug showed they were wrong or silent (a quirk goes to the board
  doc, a behaviour to its spec). A finding left for later goes to [backlog.md](backlog.md).
- **The commit message** says what the user saw and why it happened: it is the changelog's source.
- **The changelog is written at release**, from `git log <last-tag>..` ([releasing.md](releasing.md)):
  there is no `Unreleased` section; don't add one.
- Tags and releases only after the owner's OK.
