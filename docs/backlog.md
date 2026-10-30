# Backlog: technical debt

Refactors and fixes found in reviews that need more than a small edit. Not features (those are in
[wishlist.md](wishlist.md)). Each item: where, what is wrong, the suggested change, size (S/M/L)
and risk. Remove an item when it is done; add the date of the review that found new ones.

Review of 2026-10-28 (whole repo).

## Correctness

- **Config edits can be lost (race)** — `internal/app/settings.go` `apiSettings` /
  `applyConfigKeys` (sync, assets), `lightcal.go` `saveLuxCalibration`. Each reads the config
  file outside `confMu`; `applyConfigText` takes the lock only to write. A web save and a sync
  message at once can drop one change. Fix: `applyConfigText` (or a variant) takes
  `edit func(old []byte) []byte` and runs it under the lock. S–M, low risk.
- **`egress.sh lock` reports success on failure** — `yastationd/init.d/egress.sh:117`: prints
  "EGRESS LOCKED" even when `build`/`hook` failed; an unknown argument exits 0. Fix:
  `build && hook || { echo FAILED; exit 1; }`, exit 2 on bad usage. S, but safety-critical (the
  boot hook calls it): review deliberately, test on a unit.
- **`free: -1` and the upload limits** — `web/src/assets.tsx` now treats a negative `free` as
  unknown; it still ignores `max_frames` / `max_audio` from `/api/assets`, so an oversize video
  fails only after converting. Check them before the upload. S.
- **spotter-host argument parsing** — `spotter-host/spotter_host.c` `main`: `atoi` for
  `-N/-R/-r/-c` with few bounds; a negative `-r` in `run` gives a huge malloc. `strtol` with
  bounds. S.
- **spotter-host `vqe` stats period** — `cmd_vqe` (~line 420): `chunks % 62` assumes 16 ms chunks
  (orion's are 32 ms), and the speaker level reads `HW_SPK_CH` only (ignores a multi-ref `-Q`).
  Derive from `f.chunk` and the rate. S, dev-only mode.
- **viewer's "Number" button** — `internal/frametap/viewer.html` sends the volume slider's value
  as the number; looks accidental. S.
- **Two RSS readings** — `internal/app/vitals.go`: `/metrics` reads `/proc/self/statm` (not under
  `cfg().Path`), `/api/vitals` reads `VmRSS`. Use `readVitals().DaemonRSS` for both. S.

## Duplication


- **Board-overridable keys listed twice** — `internal/app/board.go` `resolveBoard` and
  `settings.go` `boardValues` spell out the same ~11 keys. One table of
  `{key, get(profile), set(profile, cfg)}`. M, low–medium risk.
- **Two config write paths** — `settings.go` `saveConfigTrial` repeats `writeConfigFile`'s
  .bak/.new/rename with the trial marker in between. Split `writeConfigFile` into backup and
  commit steps. S, medium risk (rollback semantics).
- **Child-process supervisor twice** — `internal/wake/wake.go:65-90` and
  `internal/sendspin/sendspin.go:51-75`: the same restart-with-backoff loop. One helper. S.
- **WAV header in 4–5 places** — `app/wakecapture.go` (`wavFile`), `app/input.go`
  (`wavDuration`), `app/assets.go`, `cmd/intercom-replay`, `cmd/intercom-peer`. One writer and
  parser (e.g. in `hw`). S–M.
- **Config note prefixes coupled by string** — `config/config.go` `ownNote` hardcodes the notes
  app passes to `EditNote` (settings, assets, lightcal, sync, password). Exported constants. S.
- **Wi-Fi portal re-validates config by hand** — `internal/wifisetup/settings.go:159-272`
  re-checks host, port, tz, ws URL and probes apart from the registry. Validate through
  `config.Lookup` / `ParseLenient`; keep only portal rules. M, medium risk (error texts change).
- **16 kHz declared three times** — `hw.MicOutRate`, `wake.VQERate`, `intercom.Rate`. One
  constant. S.
- **The 1..600 s duration bound** — `mqtt/discovery.go:140,149` (HA min/max) and
  `app/commands.go` (`minDurationSecs`/`maxDurationSecs`). Export one. S.
- **«громче»/«тише» mapping twice** — `app/wakeword.go:168` and `app/intercom.go:781`. A
  `voiceVolumeCommand(p) bool` helper. S.
- **Light-sensor "source" logic twice** — `app/lightcal.go:46` and `brightness.go:209`; and two
  medians of different kinds (`measureLight` takes v[n/2], `median()` averages the middle pair).
  S.
- **The LED renderer twice** — `drawRing` / `seg7Shapes` in `web/src/extra.tsx` and
  `internal/frametap/viewer.html` (hand-copied, not drifted yet; different scales). A shared
  module embedded in both, or retire the viewer for the app's LED tab. M.
- **Web: formatters and error handling** — `fmtBytes`/`fmtPair` (app.tsx) vs `fmtMB`
  (assets.tsx): one `format.ts` with `fmtSpan`, `ago`. `catch (e: any) { setErr(e.message ||
  String(e)) }` ~25 times: an `errText(e)` or a `useErr` hook. `api.diag()` repeats `req()`'s
  timeout/abort/401. S–M.
- **Call end reasons are free text** — `internal/intercom/call.go` builds "rejected: …",
  "no answer", "media error", "missed"; `app/intercom.go` matches the strings. Typed reasons or
  `Status.EndKind`. S. (`intercom.ReasonTimeout` is never sent: drop or use.)

## Size and structure

- **`app/intercom.go` `icStartAudio`** (~180 lines): VQE mode switch, queueing, test recording,
  uplink and playout goroutines. Split `icRecorder`, `uplinkLoop`, `playoutLoop`. M, medium risk
  (teardown order in `ic.stop`).
- **`app/app.go` `Run` (~165 lines) and `New` (~100)**: extract `buildDiscovery`,
  `startLogShip`, `blankScreen`, `openAmbilight`; move `ringStateJSON` & co. next to the
  commands. Magic numbers there: `hmon.Run(stop, 10s, 2s)`, `Recheck(2s)`, the 5 s
  `stopVendorLeftovers`, `restoreVolume`'s 20 × 500 ms, the web server's timeouts. M, low risk.
- **`app/input.go` holds volume and sound code** (lines 216-450: nudge/scale/percent, chime,
  `wavDuration`, unmute/announce, `sleepOrStop`). Move to `volume.go` and `sounds.go`. S (pure
  move).
- **`app/satellite.go` `OnEvent`** — inline audio-chunk branch; extract `onAudioChunk`. S.
- **`app/lightcal.go` `apiLightCal` POST** (~50 lines) mixes HTTP with the measurement; extract
  `calibrateLight(lux)`. S.
- **`mqtt/discovery.go` `Entities`** — one 259-line function; per-feature helpers. Snapshot test
  of the payloads first. M.
- **`config/registry.go` `init`** — ~400 lines; split by group (keep the order: it is the UI's).
  M.
- **`board/board.go` `set`** — a 169-line switch; the file mixes specs, the volume curve,
  parsing, validation. Split files, consider a key→setter table. M.
- **Web files** — `app.tsx` (~680 lines): `SpeakerCard`/`VitalsStrip`/`VolumeSlider` to
  `speakercard.tsx`, Login/Account to `account.tsx`; the `{tab === …}` chain as a table.
  `extra.tsx` (~800): split into `led.tsx`, `log.tsx`, `calibration.tsx`, `wifi.tsx`. M (moves).
- **Web CSS** — `.steps` serves the calibration list and the sequence editor (rename the editor's
  `.seq`); `.row > button:first-child` makes the first button of any row primary implicitly
  (make it explicit, drop `.medit .row > button.ghost`); the switch knob `#fff` is not a token.
  S–M.

## Hardcoded values

- `app/netwatch.go:143` `wlan0` → a board key (`platform.wifi_iface`) if a model differs. S.
- `app/voice.go:138` `{"name": "alisa"}` in the detection event → from the wake model or the
  profile. S.
- `spotter-host/tools/listen.sh`: bergamot's model UUIDs and paths; take arguments. S.
- `emu/run.sh:84`, `emu/seed-from-unit.sh:101`: the slirp test broker `10.0.2.100:1883` twice →
  `emu/lib/common.sh`. S.
- `hostapd/Makefile`, `dropbear/Makefile`: `-j8`. S.
- `internal/wifisetup/setup.go` `New` repeats `config.Default`'s channel and address. S.
- `docker/*.Dockerfile`: base images pinned by tag, not digest; the cross image's apt packages
  unpinned. Pin `@sha256:`. S.
- `tools/deploy.sh:68-74`: an unknown platform in the hosts file fails late with a confusing
  error; validate every line before building. S.

## Dead or test-only code

- `mqtt.NewBuilder` returns the unexported `*builder`: export `Builder`. S.
- `hw/input.go:20-25`: `KeyMute`, `KeyVolumeDown`, `KeyVoiceCommand` unused, `KeyVolumeUp` /
  `SwMuteDevice` test-only (pre-profile leftovers). `hw.FindInput`, `hw.MasterControl`:
  test-only. S.
- `GET/PUT /api/config` (the raw config editor): the app no longer calls it; keep as an API or
  drop. S, decide.
- `api.login` returns `defaultPassword`, unused: show a "change the default password" hint, or
  drop it. S.
- `app/wakeword.go:84`: "No verified AEC yet" as the reason to ignore a wake during our own
  playback; the AEC is measured now (docs/intercom.md). Revisit the rule. S, design call.
- staticcheck ST1020-22: ~25 doc comments not of the form "X does …". Only in a sweep.

## Docs

- `yastationd/README.md:204-290` "Open items" is mostly history (item 5: auto-brightness, item 7:
  Wi-Fi hangs now handled); move it to sensor-calibration.md / storage.md / the changelog, keep
  the open ones. M.
- `docs/assets.md:3`, `docs/video-mode.md:7,121` say the feature has not run on a unit; update
  after the next check on a Station 3. S.
- [docs/index.md](index.md): [boards/_template.md](boards/_template.md) has no entry (linked from boards/README.md). S,
  optional.

Found by the docs simulation of 2026-10-30 (agents reading the docs and code for typical tasks):

- `internal/wake/wake.go`: the child's stderr is not captured (`cmd.Stderr` unset), so the
  spotter's model-load errors and "spotter lagging" never reach the log. Pipe it to the log as
  `voiceid.Host` does. S.
- `internal/health/health.go`: one failed probe round flips `health` / `pipeline_ok` in HA; only
  the lifecycle's 10 s grace smooths the ring. Debounce the entities the same way. S.
- The heartbeat file (`heartbeat_file`) is written every 10 s and read by nothing but `diag.sh`.
  Either use it (the supervisor's liveness check) or say in health.md what it is for. S.
- Stale comments: `spotter-host/spotter_host.c` header says serve's stdin is "8ch S16 48 kHz"
  (it is per board, `-N`; orion 13 ch at 96 kHz); `internal/app/power.go` says the speakers want
  a 12 V contract (orion takes 20 V, `min_mv` 15000); `internal/hw/power.go` says `ya_pd` is on
  bergamot and pickle (orion has it too); `wakeword.go` and wake-arbitration.md give "AEC
  unverified" as the reason a wake during playback is ignored, while the AEC has been measured
  (intercom.md). S.
- `/metrics` reads the daemon's RSS from `/proc/self/statm`, vitals from `/proc/self/status`: one
  source. S.
- The two example configs use different probe placeholders (`homeassistant.local` vs
  `<ha-host>`). S.
