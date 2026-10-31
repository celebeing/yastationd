# Changelog

Versions follow [SemVer](https://semver.org/); before 1.0 a minor version adds features and a
patch version fixes things. How to cut a release: [docs/releasing.md](docs/releasing.md).

## v0.24.1 — 2026-10-31

**Fixes**
- Two shared settings changing together could send every speaker into an endless exchange of old
  values, rewriting `yastationd.conf` many times a second: wear on the flash, high load, a log that
  turned over in a minute. Two edits of the config no longer undo each other, and a value taken
  from the other speakers is never sent back.
- A fuse for whatever else could start that: a shared item changing hands too often pauses the
  sync on that speaker, 10 minutes and up. A slider dragged on one speaker doesn't trigger it.

**The processor's load as a percentage**
- The web app's card shows CPU, 0-100 %, in place of the load average, which reads high on these
  speakers for no reason. `/metrics` gains `yastation_cpu_percent` and keeps `yastation_load`.

## v0.24.0 — 2026-10-31

**A factory reset that lands in the CFW** ([docs/guides/golden-image.md](docs/guides/golden-image.md))
- `tools/golden.sh` builds a restore image from a speaker running the CFW and writes it to
  `aml_sysrecovery`, so the vendor reset gesture brings the unit back into the CFW, without keys,
  Wi-Fi or settings. Tried on a Mini 3 Pro, reset and restore included; Station 3 is untried.
- A speaker with no saved Wi-Fi network enters the setup mode by itself.
- The setup page adds SSH keys (`<cfw>/authorized_keys`, read by OpenSSH next to the rootfs's file).
- `tools/backup-unit.sh` and `tools/restore-unit.sh` leave a golden image's own files alone.

## v0.23.0 — 2026-10-31

**OpenSSH on the speakers** ([docs/ssh.md](docs/ssh.md))
- The speaker's SSH server is now OpenSSH 10.6 (our static build on OpenSSL 3.5 LTS): log in with
  a certificate signed by your own CA (a CA key file and a principals file on the speaker, none
  ship with the CFW), SFTP, and `scp` from current OpenSSH clients without `-O`. The host key is
  dropbear's, so `known_hosts` doesn't change.
- `tools/deploy.sh` switches each speaker carefully: the new server is tried on port 2222 first,
  port 22 switches only after a login through it, and a login on 22 confirms or the old server
  comes back. Then the boot starts it too. `SSH=dropbear` keeps dropbear.
- dropbear stays on the speaker as the fallback: it starts whenever OpenSSH can't.

## v0.22.3 — 2026-10-30

**Logs off the speaker** ([docs/storage.md](docs/storage.md))
- While the log server is unreachable, logs are appended to the spool on flash instead of rewriting
  all of it every minute, which could reach 1.4 GB a day with a server down for long or a wrong
  `log_remote`.
- The spool holds 16 MB by default (was 1 MB): about a week of a quiet speaker's logs, at no extra
  wear. At the limit the older half goes.
- A large spool goes out in chunks after an outage and resumes where it stopped if the link breaks
  again.

**Docs** — a rule for flash wear in the development guides, and an audit of everything the daemon
writes to flash in [docs/storage.md](docs/storage.md).

## v0.22.2 — 2026-10-30

**Voice ID** ([docs/voice-id.md](docs/voice-id.md))
- The voice log wears the flash far less: a full log used to be rewritten whole (~9 MB) on every
  request; now it is rewritten about once per 1500 requests.
- Limits per group instead of one for the whole log, so a voice heard rarely is not pushed out by
  one heard all the time: 1000 requests for a named or household group, 100 for an unnamed one,
  100 of "not a person", 20 unnamed groups. Nothing to migrate.
- A group's name and household mark survive however long the log runs, and new requests no longer
  fall out of a log full of pinned entries.
- People tab: "Show more" loads the next page instead of everything again, and no longer collapses
  the list to 100 rows at the end.

**Docs** — a map of the architecture; specs, user guides and research apart, each with an index;
guides for adding a feature and fixing a bug; a link check.

## v0.22.1 — 2026-10-30

**Packs** ([docs/asset-packs.md](docs/asset-packs.md)) — with rlex (#4)
- Import several packs at once: each archive is checked and installed by itself, and a rejected
  one doesn't stop the rest. A newer version of an installed pack updates it; the same version is
  skipped; the page says which happened.
- **Preview**: every animation of a pack loops in the browser, nothing sent to the speaker,
  painted by the same code as the LED page's preview. **Play on the speaker** still opens them on
  the LED page.

**Tests** — unit tests for the MQTT client, the Wyoming server, health, the player, the Wi-Fi
setup portal, Sendspin and more, by rlex (#6).

## v0.22.0 — 2026-10-30

**Voice ID** ([docs/voice-id.md](docs/voice-id.md))
- The speakers tell who is talking, with the firmware's own voiceprint model, on the speaker (no
  server, no cloud). Off by default: `voice_id = true`, and `voice_store = true` on one speaker.
- HA gets, per speaker, who it was (a person of the household, guest or unknown), the speaker's gender (no
  enrollment needed) and whether the voice is the household's, before it acts on the request.
- **Ignore guests** (an HA switch and a web app option): a request from a voice the household
  doesn't know never reaches HA; the new `voice_guest` moment shows it was heard and ignored. The
  check waits for «Алиса» and the start of the request (`voice_hold_ms`), with the speech held, so
  HA hears the end of the request no later than before.
- The People tab: people appear by themselves as the speakers hear requests, like faces in a photo
  album; name them, mark who is the household (a name alone doesn't), merge, move a misheard
  request, forget a person. Ignore guests and the guest threshold are there too, with a preview of
  what a threshold does to the last requests.
- How to tell HA's assistant who is talking: one line in the agent's prompt
  ([docs/guides/ha-assistant-setup.md](docs/guides/ha-assistant-setup.md)).
- `make voice-models` for the Mini 3 0.6.x firmware, whose own voice model is too old.

**Station 3's rear light shows HA's colours as meant** ([docs/ambilight-color.md](docs/ambilight-color.md))
- The strip was driven with HA's RGBW as is, and its colours were visibly off (a strong blue, a
  greenish white). Measured with a camera, the board profile now carries the strip's primaries and
  response, and the speaker solves for the drive that gives the wanted colour.
- The HA light is now colour + colour temperature (2000-8000 K) instead of RGBW; a colour set
  before is kept. `ambilight_white` and `ambilight_calibration` settings; music effects get the
  corrected colours too, DDP streams stay raw.

**Build**
- The host needs only docker, sh, make, git and ssh: `make sounds`, `make clips`, `make video`,
  `make pack`, `make stage` and the LED demo stages now do their Go/ffmpeg/zstd work in pinned
  containers (a new `yastation-tools` image), fetching from and uploading to the speaker over ssh
  on the host. `tools/build.sh packtool` / `hosttool:<cmd>` build the host tools (the pack
  checker, ledsim) for this machine; `make:<target>` runs a conversion in the tools image.
- `packtool -get <field>` and `-index <url>` read pack manifests, so the asset repo's release
  scripts need no jq.

## v0.21.2 — 2026-10-29

**Your own packs** ([docs/asset-packs.md](docs/asset-packs.md))
- The built-in behaviour is now level 0, always at the bottom; above it the packs a speaker wears.
  Your changes live in packs of your own: make any number, rename, reorder, turn on and off,
  export. "Your own" is the first, and it is worn on top unless you place it elsewhere.
- Moments: pick the pack you edit ("Edit in"); each moment shows the layer it comes from;
  **Customize** copies what a moment does now into your pack to change it there; "Remove from"
  lets the layers below show again. A sound or an animation can be several, picked at random
  ("+ or").
- Installed packs are read-only; **Copy to my pack** makes an editable copy in its place.
- Sounds: add and remove sounds in any of your packs.
- The web app's Assets section is now **Behaviour**, with the Moments editor as its first tab and a
  short explanation of moments, packs and sounds above them.

**Changed**
- Nothing about packs or moments is shared between speakers any more: each speaker has its own
  packs and theme (one **Apply** puts a pack on top of this speaker's theme). A theme or a moment
  shared before is ignored. Sharing comes back through exporting and installing packs.
- `<cfw>/moments.json` is folded into the pack "Your own" at the first start and renamed
  `moments.json.old`.

**Fixed**
- The Packs page made it too easy to apply a pack to every speaker with one click, with no undo,
  no error, and nothing worn where the pack was missing.

## v0.21.1 — 2026-10-29

**Packs**
- The LED page browses the animations in a column view, like a file browser's: the firmware's own (with their subfolders) and
  one folder per installed pack, played with a click without applying the pack, plus a search.
  The Packs page links each pack's folder.
- **Apply here** wears a pack on this speaker only (`theme_here`, never shared); "Wear the shared
  theme" goes back.

- A moment can pick at random: a sound, an animation or an expression may name several
  (`neco_short_1,neco_short_2`), and each showing plays one of them.

**Fixed**
- A sound on the listening moment played twice after «Алиса» (once more when HA started
  transcribing): a voice stage fired again now goes on instead of starting over.
- A pack copied into `<cfw>/packs/` by hand did not show up until the daemon restarted; the Packs
  page now picks it up.

## v0.21.0 — 2026-10-29

**Themes and asset packs** ([docs/asset-packs.md](docs/asset-packs.md))
- Everything the speaker does when something happens is a moment you can change: the faces, the
  ring, every sound, and the status lights too (booting, HA lost, mute, calls, the timer's alarm,
  Wi-Fi setup). The default theme does what the CFW did before.
- A moment's sequence step can light the ring (any colour and period), show an animation or text
  and play a sound at once: mixed over everything, or said as speech. Steps run one after another;
  an empty step is a pause, a held one repeats its sound (ring tones).
- New moments, silent by default, for the places assistants beep: the wake word (`listening`), a
  volume key at its end, a timer started, mic unmuted, HA back, a missed call, and more.
- **Assets → Packs**: install packs of animations, sounds and moments (a .zip, checked on the
  speaker), order them as a theme (shared like a setting), copy a pack's moments into your own,
  save your own as a pack. **Assets → Sounds**: play, add (any audio, converted in the browser)
  and replace sounds. The Moments tab shows which pack sets a moment.
- `make pack PACK=<dir>` (`cmd/packtool`) builds and checks a pack on the host.

**Changed**
- The Display faces switch is gone: the theme decides what the front display shows. A config that
  still sets `display_faces` loads as before (the line is ignored); HA drops the entity.
- The board profiles no longer carry sound paths (the stock sounds are played by name from
  `<cfw>/sounds`); an old `board.conf` with them still loads.

## v0.20.4 — 2026-10-28

**Deploying**
- One way to update speakers: `tools/deploy.sh`. `make deploy` is now a shortcut to it for the
  speaker in `local.mk` (it used to build on the host and ship the daemon alone, with no check
  that it came up); `make deploy-spotter` and `make deploy-sendspin` are gone (the script ships
  both). The first install ends with `DROPBEAR=1 tools/deploy.sh` after `make install-initd`.

## v0.20.3 — 2026-10-28

A review pass over the whole repository (the larger findings are in `docs/backlog.md`).

**Fixes**
- The light level reaches HA in the dark: 0 lx was never published, so HA kept the last
  non-zero value.
- The LED viewer (`/tap/`) no longer puts its `?src=` into the page as markup.
- Scripts that run on a speaker no longer dump every mixer control or read
  `/proc/net/wireless` (the commands that froze a Mini 3): `tools/board-survey.sh`,
  `tools/orion-verify.sh` (only with `FULL_MIXER=1`), `diag.sh -x`.
- HA's Wyoming integration shows the speaker's own model, not always "Station Mini 3".
- Assets: when the speaker cannot tell its free space, uploads are no longer refused.
- spotter-host: unchecked allocations, a leaked buffer.

**Web app**
- A visible focus ring for keyboard use, labels on every control; the Wi-Fi "connected" badge
  is green again; the delete button stays readable in the dark theme; uptimes read the same
  everywhere. The Wi-Fi portal and the LED viewer take the current palette.

**Inside**
- Dead code removed, repeated code behind helpers, magic numbers named, missing doc comments
  added; `docs/` brought in line with the code (API table, intercom end reasons, the Station 3's
  CFW path, both example configs list every key — a test keeps it so).

## v0.20.2 — 2026-10-28

**Home Assistant**
- HA's device card loses what the web app does better: the Ring animation and Expression selects
  (the web app's LED page plays both), and the speaker's own settings (Text duration, Animation
  duration, Display faces, Dialog follow-up), which stay on the web app's settings pages. Their
  MQTT topics stay for automations. HA drops the entities by itself (an empty discovery config).
- The ring as an HA light is opt-in: `ha_ring_light` (default off); its MQTT topic works either way.

**Web app**
- The LED page plays the ring's effects, and draws each ring as it looks: a blended circle
  (Mini 3 Pro), a blended square along the Station 3's edge, a small pie of sectors (Mini 3); the
  same in `cmd/ledsim`.

## v0.20.1 — 2026-10-28

**Voice**
- `preroll_ms` defaults to the board's value, measured for its wake word model
  (`wake.preroll_ms`): 150 ms on the Mini 3, where «Алиса» ends ~110 ms before the spotter fires
  (real recordings: the wake word left HA's transcripts, 9 of 9), 220 ms on the Station 3 (its
  verifier fires ~170 ms after the word ends); the Mini 3 Pro keeps 300 until measured. The key is
  no longer shared between speakers.

## v0.20.0 — 2026-10-28

**Web app**
- **Assets → Videos** (speakers with a pixel screen): a library of clips for the video mode.
  Convert a video file right in the browser (frames and a WAV, no ffmpeg) or upload what
  `cmd/videoconv` made; play one now, pick the one the mode plays, delete
  ([docs/assets.md](docs/assets.md)).
- The speaker card's controls lose Faces (it stays under Display and voice).

## v0.19.0 — 2026-10-28

**Voice**
- The audio for Home Assistant starts from the point where the spotter heard «Алиса»: spotter-host
  reports the WAKE's sample in the vqe_0 stream (`AT` lines, with the blocks it dropped counted),
  and `preroll_ms` counts back from it, not from when the session subscribed.
- `wake_word_in_stream`: send the wake word too (the stream starts 1.3 s before that point).
- `wake_capture` (a test option): saves the audio around every wake word under `<cfw>/wakes`, to
  measure where the wake word ends in real speech ([docs/wake-word-boundary.md](docs/wake-word-boundary.md)).

**Sync**
- A shared setting left at the default is shared as the default, not as its value: every speaker
  keeps its own build's default, so a default changed in a new version is no longer pinned by an
  earlier share (the fleet kept `arbitration_wait_ms = 700` after v0.18.1 made it 300).

**Diagnostics**
- `<cfw>/bin/diag.sh` (shipped by deploy.sh) packs everything useful about a speaker into one
  tar.gz, redacted by default for bug reports; the web app downloads it (Status → Log →
  "Download diagnostics", `GET /api/diag`). `tools/diag-build.sh` does the same for a machine
  whose build or deploy fails ([docs/guides/diagnostics.md](docs/guides/diagnostics.md)).

**Web app**
- A logo (a deltoid on a deep-blue-to-sky disc) in the README, the favicon and beside
  "Speakers"; the app's accent moves from violet to the logo's blue on cool neutrals, the status
  colours get dark-theme versions, and [docs/design.md](docs/design.md) sets the rules.
- The speaker card: a vitals panel beside the name (the speaker's and the daemon's uptime apart,
  load, temperature, memory used, the daemon's memory, flash used, each with a thin gauge;
  `GET /api/vitals`, and
  `/metrics` gains `yastation_system_uptime_seconds`), the controls in one toolbar row under it.
- "Auto brightness: dimmest" sits right after "Clock brightness" and can be shared between speakers.

## v0.18.1 — 2026-10-28

**Fixes and operations**
- `tools/build.sh` builds a builder image only when it is missing or its Dockerfile changed
  (a label on the image), with the progress shown; it no longer leans on docker's build cache,
  which, once broken, rebuilt the zig image on every target.
- The nearest-speaker wait for a quiet claim is 300 ms (was 700): in two days of logs the
  speakers' claims came at most 141 ms apart, and every request one speaker alone heard waited
  the full time.

## v0.18.0 — 2026-10-28

**Settings**
- The web app's settings are rebuilt on one registry of config keys: every setting has an
  explanation, and most apply at once — the config is reloaded live, a restart (on trial) only
  for keys that need one (marked; one floating "Apply and restart"). The **Configuration** group
  is by topic: General, Voice, Moments, Home integration, Music Assistant, Intercom, Calibration,
  Wi-Fi, Login; rarely touched keys are folded under "Advanced" on their own page. The raw config
  editor is gone from the app (every key is on a page; the API stays).
- **Share with every speaker:** a switch on each setting, option and moment gives all speakers
  this speaker's value and keeps them in step (retained MQTT topics; a speaker that was off
  catches up; moments go to speakers with the same kind of display).
- A speaker card on top of every tab: name, address, status, volume with mute, display mode.
- **Auto brightness has a range:** from a dimmest level (`auto_brightness_min`, else the
  board's) to the clock brightness, which is now also the auto maximum; setting it no longer
  turns auto off. Speakers with auto on keep their behaviour (their clock brightness becomes 255).
- The ambilight's music effects use the strip's white LEDs by default (`ambilight_fx_white`),
  and the settings list only what the board has (no ambilight or video keys on a Mini).

**Fixes and operations**
- `tools/fetch-vendor.sh <ssh-target> <board>` copies what the build needs off your own speaker
  (read-only): the sysroot spotter-host links against and the stock animations for the
  simulator, into the gitignored `vendor-local/`, and names it in `tools/build.env`.
  `tools/build.sh` uses a lone tree there by itself, and `tools/deploy.sh` fetches one from the
  first speaker of an arch that has none — a fresh checkout deploys with nothing set up. A
  variable set in the environment now wins over `tools/build.env`.
- A config line this version refuses (a value an older build took, an unknown key) no longer
  keeps the speaker from starting: it is logged and skipped, its key keeps the default, and the
  web app shows it.
- The zig builder image takes the zig build for the host's own arch (aarch64 hosts too), checked
  by sha256; source downloads retry.
- Research notes: voice ID with the firmware's own models, ready-made server proxies, and how the
  big assistants enroll voices (docs/voice-id.md).

## v0.17.0 — 2026-10-27

**New**
- Moments are the owner's to choose: the web app's Moments tab edits what each moment shows — an
  expression, an animation of the front display, a scenario, or a sequence of steps of its own
  (expressions, animations, ring effects, text; the last step can hold) — with a fallback list,
  an unsaved preview and a reset. Kept in `<cfw>/moments.json`, applied at once.
- The voice stages (`listening`, `thinking`, `speaking`, `waiting`) are moments, so the
  activation chain on Station 3 or the bars on the minis can be redrawn; the voice ring stays
  under a front-only choice.
- Scenarios and moment sequences share one runner: a step's length may be left out (the
  animation's own), and a sequence stops when anything else takes its slot.

## v0.16.9 — 2026-10-27

**Fixes and operations**
- The web app's login survives restarts, redeploys and config saves: tokens are signed with a
  secret the speaker keeps (`<cfw>/web.secret`, written once) and its login, instead of being
  held in memory. Changing the password still logs everyone else out.
- dropbear, the SSH server, is ours now: `tools/build.sh dropbear` builds 2026.94 from the
  pinned release (static aarch64, every board, with scp), and `tools/deploy.sh` puts it on a
  speaker on its first deploy or with `DROPBEAR=1` (`DROPBEAR=only`: nothing else), trying it on
  port 2222 before the swap. The
  speakers ran 2022.83, which predates the Terrapin fix (2024.84) and has no scp.

## v0.16.8 — 2026-10-27

**New**
- The LED preview shows the minis' 7-segment clock too: the frame tap streams a drive level per
  element (`seg7` in `/info` and in each frame), and the web app's LED tab and the standalone
  viewer draw it, after a photo of a Mini 3 with every element lit. `cmd/ledsim -board bergamot`
  simulates a mini. The web app draws the front display as on the glass: the light diffused and
  seen through the fabric's weave.
- `cmd/demoreel` records a tap's front display into a GIF as the glass shows it (diffusion, the
  fabric's weave); the README opens with two reels made with it (docs/led-preview.md).
- The README is in Russian; the English one is `README.en.md`. Both have an install section.

## v0.16.7 — 2026-10-27

**Fixes and operations**
- Station 3's voice front-end ran without its noise reducer model: the daemon pointed it at an
  empty `<cfw>/vendor/vqe_models` (left by a `make vqe-models` that found no `model_call.nnet` on
  firmware 3.21). It now uses the CFW's directory only with the model in it; Station 3 uses its
  firmware's own `noise_reducer/model.nnet`, which is what its library loads, and
  `make vqe-models` does nothing there. This is what the intercom's call mode needs on Station 3.

## v0.16.6 — 2026-10-27

**Fixes and operations**
- Light sensor calibration from the web app (Settings → Calibration): enter the light at the
  speaker's top and press Measure; the speaker measures for 15 s with its own lights off and
  stores the point in its config, applied at once (a second point in clearly different light
  gives the slope). Two ways: an incident light meter (lux, or EV / an exposure, converted with
  C = 250 — checked against a Sekonic to 1 %), or a phone or camera metering a sheet of white
  paper on the speaker in reflected light (converted for 85 % reflectance, K = 12.5). The page
  converts as you type.
- The web app's speaker view: a section menu beside the content (Status, Controls, Settings), the
  layout filling the window, settings and controls as narrow settings pages (name and control on
  one line), the open section in the address (`#options`).

## v0.16.5 — 2026-10-27

**Fixes and operations**
- The web app has a Log tab: the tail of the daemon's log or of the system log (kernel, Wi-Fi
  driver), filtered, following as it grows.

## v0.16.4 — 2026-10-27

**Fixes and operations**
- The network watchdog never fired on a speaker with a broker configured: it took the MQTT
  client's "connected or reconnecting" for connected. It now checks the open connection (the wake
  arbitration had the same mistake: with the broker gone it still waited for claims).
- The network watchdog first tries what the stock firmware's Wi-Fi watcher does: after 2 min
  without the gateway it reloads the Wi-Fi configuration, after 4 min (then every 10) it resets
  the Wi-Fi controller with the firmware's `yandex-wifireset` (driver out, SDIO power cycled,
  back in), and reboots only at `net_watchdog_min`. A hung Wi-Fi chip ("fw recovery not finish")
  took the black Mini off the LAN this way. Profile key `platform.wifi_reset`; config
  `net_wifi_recovery`, `net_wifi_reconfigure_min`, `net_wifi_reset_min`, `net_wifi_reset_every_min`.

## v0.16.3 — 2026-10-27

**Fixes and operations**
- Building `sendspin-host` in a checkout that the old builder (before v0.16.2) had built failed:
  its CMake cache pointed at `/src`. The Makefile now starts such a build directory over.

## v0.16.2 — 2026-10-27

**Fixes and operations**
- Every artifact builds in a pinned container of its own toolchain (`docker/`: Go, Node, zig,
  the cross gcc) through one script, `tools/build.sh`; the host needs docker only.
  `tools/deploy.sh` builds what it ships with it — yastationd, hostapd, sendspin-host and
  spotter-host (with a sysroot named in `tools/build.env`) — instead of taking helper builds as it
  found them. `NATIVE=1` keeps the host's Go for yastationd.
- `tools/deploy.sh` stops at an ssh failure and says it is one (login refused, unreachable), not
  "not installed here"; every upload is checked.

## v0.16.1 — 2026-10-27

**Fixes and operations**
- Station 3's rear strip gets its white channel through the framebuffer: the firmware's device
  tree sets the strip's framebuffer up without white (`ledfb-rgb48`); yastationd switches it to
  `ledfb-rgbw64` with a runtime device-tree overlay (until the next boot, nothing on flash) and
  drives R, G, B and W in one write. RGBW from DDP and the HA light is visibly brighter. Falls
  back to RGB through the framebuffer, then sysfs. Profile keys `ambilight.fb_format`,
  `ambilight.fb_compatible`.

## v0.16.0 — 2026-10-27

Effects on Station 3's rear strip.

**New**
- A DDP receiver (UDP 4048, `ddp_port`) for the rear strip: LedFx, WLED tools or xLights drive it
  as a 12-pixel strip (RGB, RGBW accepted, pixel 0 at the top). A stream takes the strip over as
  sent; 2.5 s after it stops the strip is back. DDP has no discovery: the settings are in
  `docs/ambilight-stream.md` and in the web app's LED preview tab.
- Music effects on the strip while music plays on the speaker, drawn from sendspin-host's
  spectrum: `rain`, `vu`, `spectrum`, `energy`, `scroll`, `pulse`. HA / web app: the Ambilight
  music effects switch, effect select and brightness. `ambilight_fx_white` (off) lets them use the
  white channel.
- `cmd/ddpsend` streams test effects to a speaker from a computer.

**Fixes and operations**
- The strip's R, G, B go through its framebuffer (`ambilight.fb`): a frame costs 3.5 ms instead
  of 25–34 ms of sysfs writes. Its white stays on sysfs: the device tree configures the
  framebuffer without white.

## v0.15.0 — 2026-10-26

Music on the display.

**New**
- A new track's title ("Artist - Title") runs once over the front display; not on a resume, not
  for a track first seen mid-play.
- Pause and resume animations (moments `media_pause`, `media_resume`), from the track's playback
  speed; they sit under the title, so a skip shows only the new title.
- Station 3: while music plays, the clock in small digits over a 21-band spectrum of the music,
  computed by `sendspin-host` from the PCM it plays and timed to when it is heard.
- `music_title`, `music_clips`, `music_levels` switch them (all on); `marquee_speed` sets how fast
  running lines move.
- `sendspin-host` takes the Sendspin metadata role and reports `PLAYBACK`, `META` and, with
  `-b`, `LEVELS`.

**Fixes and operations**
- Running lines enter at the middle of the display, so their start is not lost behind the
  animation before them.
- The example configs gave the auto-brightness curve in the old reading units; it is in lux.

## v0.14.0 — 2026-10-26

**New**
- The web app's LED preview plays any of the speaker's screen animations (a filterable list, loop,
  clear) and its expressions, as the old frame-tap viewer did.

**Fixes and operations**
- The web app is no longer committed: `make build` (and `tools/deploy.sh`) builds it in the pinned
  builder container whenever `web/` changed, so the build host needs docker, not Node. A plain
  `go build` still compiles; that binary serves a page saying it was built without the app.
- `tools/deploy.sh` / `make deploy`: `SSHKEY` is optional; without it ssh uses the agent and
  `~/.ssh/config`.

## v0.13.3 — 2026-10-26

**Fixes and operations**
- The web app's update check could show an old release for up to an hour (it kept GitHub's answer
  for the tab). It keeps it for 10 minutes now, asks again at once when a speaker reports a newer
  version, and links the release page.
- Every version tag gets a GitHub Release with its changelog section (a GitHub Actions workflow;
  run by hand, it creates the missing releases for older tags).

## v0.13.2 — 2026-10-26

**Fixes and operations**
- A fresh checkout of v0.13.0/v0.13.1 did not build (`internal/webui/webui.go: pattern dist: no
  matching files found`): the built web app was swallowed by a `.gitignore` rule and never
  committed. It is committed now, and `make build` / `make test` build it first if it is
  missing.

## v0.13.1 — 2026-10-26

**Fixes and operations**
- `/metrics` reports the speaker's memory (total, available, free), its load average and the SoC
  temperature (every thermal zone).

## v0.13.0 — 2026-10-26

A web app on every speaker.

**New**
- Every speaker serves a web app at `http://<speaker-ip>/` (`http_addr`, default `:80`). Login
  **admin / yastation** until changed from the app (a PBKDF2 hash in the config). It finds the
  other speakers by itself and manages all of them from one page: sensors, the runtime controls
  HA has (the lights stay HA's), a live LED preview, the moments and what each shows, the saved
  Wi-Fi networks (adding one never touches the connection in use), the config editor and the
  login, and an update check (the browser asks GitHub; the speakers stay offline).
  docs/web-ui.md.
- Saving the config from the app is safe: it is validated first, the speaker restarts with it on
  trial, and goes back to the old one by itself if it cannot listen or ends up less healthy.
- Prometheus metrics at `/metrics`.
- The app is embedded in `yastationd` (a deploy updates it); `make web` rebuilds it, in a pinned
  container when there is no Node (`tools/build-web.sh`).

**Changed**
- The Wi-Fi setup page is served by the same web server on the access point's address.
- The LED preview runs behind the login on the web server (`led_preview`, on by default);
  `frametap_addr` still opens the standalone tap for development. Dim LEDs are now visible in it,
  and Station 3's screen is drawn as gapless square cells.

## v0.12.1 — 2026-10-26

**Fixes and operations**
- A speaker that loses the wake-word arbitration shows it: the Mini 3 and Mini 3 Pro glance
  aside (` > >`, then `< < `), Station 3 plays its stock deactivation animation. It is a moment
  (`arbitration_lost`), so it can be re-mapped per board.
- A new face in the expression bank: `angry` (seg7: eyes in the outer digits, brows bent down to
  the middle).

## v0.12.0 — 2026-10-26

The nearest speaker answers; «стоп» always ends the session.

**New**
- Wake-word arbitration: speakers that hear one «Алиса» agree over MQTT which one answers — the
  loudest, compared in calibrated dB SPL; the others blink and stay silent, and nothing said
  after the wake word is lost while they decide. On by default (`arbitration = false`).
  docs/wake-arbitration.md.

**Changed**
- «Стоп», «хватит», «отмена», «выключись» end a voice session in every phase: listening (after a
  wake word too, so «Алиса, стоп музыка» no longer reaches HA — say «выключи музыку»), waiting
  for HA, speaking. Before, a «стоп» while listening went to HA as a request.

**Fixes and operations**
- `tools/deploy.sh` installs hostapd from a recorded build in the vendor tree (`VENDOR=`) when
  there is no local one, and warns about a speaker left without it.

## v0.11.0 — 2026-10-26

Station 3 writes text in its own fonts.

**New**
- Text on Station 3's screen (`show_text`, the message mode, display commands, numbers, timers)
  in pixel fonts made for it: Latin and Cyrillic, digits, punctuation and symbols (° % « » — №
  …); numbers and timers in the clock's digits. `text_lines = auto|1|2`: one line if it fits,
  else two lines wrapped at whole words, else a smooth scrolling line. It was the seg7 picture
  before, with no Cyrillic. The minis are unchanged.

## v0.10.1 — 2026-10-26

**Fixes and operations**
- The Mini 3 and Mini 3 Pro no longer offer the `clock_date` display mode, which their four
  seg7 digits cannot show (it showed the plain clock); it stays on Station 3.

## v0.10.0 — 2026-10-26

Logs off the speaker, a network watchdog, smooth brightness, sensors in lux and dB.

**New**
- Logs to a syslog server on the LAN (`log_remote`, off by default): the daemon's and the system's
  lines (kernel included) as RFC 5424 over TCP with their own timestamps; while the server is
  unreachable they are kept in a capped spool on flash (`log_spool_kb`) and sent when it is back,
  including lines that went into a dead connection. docs/storage.md.
- Network watchdog (`net_watchdog_min`, default 10, 0 = off): with the gateway unreachable that
  long the speaker logs the kernel's tail and reboots; the wait doubles after each such reboot,
  up to 4 h.
- Light in lux: a calibration per board (`lux.calibration`) and per speaker (`lux_calibration`),
  measured against a lux meter; the speakers differed up to ×9 and now agree within 4 %.
  `Illuminance` is lux, with the raw `reading` as an attribute.
- Room sound in dB: `mic.spl_offset` per board (measured against a noise meter, rough) and
  `mic_spl_offset` per speaker. docs/sensor-calibration.md.
- `matrix.min_brightness` per board (Mini 3, Mini 3 Pro 4; Station 3 2).

**Changed**
- **`auto_brightness_curve` is in lux now.** A curve written before is in one speaker's old
  reading units: convert it with that speaker's calibration, or drop it (the default is
  `0.7:<min>, 9:128, 150:255`).
- Auto-brightness is smooth: a median against spikes, a longer average, and the level glides to
  its target at an even pace instead of jumping.
- Station 3 dims its screen through the backlight, so the clock keeps its half-tones in the dark.

## v0.9.1 — 2026-10-26

Orion shows the time right after boot.

**Fixes and operations**
- Orion's clock shows the time at boot instead of a blinking colon until the SNTP check: its
  pcf8563 RTC keeps the time without power (a new board profile flag, `platform.rtc_backed`).
  A clock before 2026 (the RTC lost it) still waits for the check. The minis have no such RTC
  and are unchanged.
- Docs: a storage and logs audit of all boards (docs/storage.md); orion's sensors survey (no
  colour sensor; the touch top's raw capacitance, the RTC, the board EEPROMs, the ADC).

## v0.9.0 — 2026-10-26

Room sound sensors in HA, and a wake word that survives a dead capture.

**New**
- Room sound sensors (the first step of "someone is asleep here", docs/wishlist.md): `Sound
  floor`, `Sound level` (dBFS) and `Sound activity` (%), once a minute from a raw mic channel,
  only levels, never audio; re-sent on a change or every 15 min. `room_sound = false` turns them
  off.

**Fixes and operations**
- A mic capture that ends on its own is reopened: on orion `arecord` exited after ~6 h and the
  wake word stayed dead until the daemon restarted. arecord's last words now go to the log.
- `Illuminance` is sent on a 10 % change or every 15 min, not every 30 s.
- `Mic mute` shows On/Off in HA instead of Problem/OK.

## v0.8.4 — 2026-10-26

Orion's volume ring: fine when slow, quick when fast; Music Assistant no longer fights it.

**Fixes and operations**
- Orion's volume ring: a step per pad crossing (the touch ring has about ten pads per turn and no
  finer position), sized by speed like a mouse wheel — 1 % slow, up to 5 % fast. It was a stock
  5 % level per 1/20 turn, ~4 dB mid-scale, and a quick turn jumped loud. Keys and voice keep 5 %.
- Music Assistant's volume events no longer throw the level back while the speaker is turned:
  they are ignored for 1.5 s after a local change and when within 1 % of the level (the server
  echoes our reports late and sometimes rounded), and a level the server set is not reported
  back to it.

## v0.8.3 — 2026-10-26

Volume: orion's stock curve, and a cap for any speaker.

**Features**
- `volume_max` (yastationd.conf): the loudest a speaker may go, in % of its curve. The percentage
  goes through the board's curve first and the result is scaled so that 100 % lands there; the
  0..100 scale in HA and on the display stays whole.

**Fixes and operations**
- Orion's volume follows the firmware's own curve (`platformToAlsaMap`): quiet steps at the
  bottom, fine ones at the top. It was a straight line over the raw range, loud from the first
  step. The Mini 3 and Mini 3 Pro have had their stock curve since v0.3.0.

## v0.8.2 — 2026-10-26

Orion: the volume scene and the Wi-Fi setup mode, verified on the unit.

**Features**
- Orion's volume: the exact level in the clock's digits, cross-fading from the old value, over a
  full-width bar whose last pixel is partly lit (1 % moves it); the bar glides, its end pixel
  flashes when pushed past 0 or 100. The firmware's volume patterns were tried and dropped: they
  sweep through the tens only. Other displays keep the number.
- The Wi-Fi setup mode's key combo is now the same on every board: **mic muted + the voice key
  held for 5 s** (it was volume-down + voice + mute, impossible on orion); the ring fills with
  yellow while it is held. Only the voice press needs the mute (orion's held mute flips back by
  itself). Verified on orion: the access point, a phone joining, "Connect now".

**Fixes and operations**
- The volume level no longer blanks the screen on every step: the bubble that is up is extended
  instead of pushed again (a new layer made the screen fade in from black).
- The daemon logs mic-mute changes and the setup hold.

## v0.8.1 — 2026-10-26

Orion's visuals polished: a clock of its own, moments, the LED preview and a video party trick.

**Features**
- LED preview ([docs/led-preview.md](docs/led-preview.md)): `frametap_addr` streams the screen,
  ring and ambilight exactly as drawn and takes the daemon's commands over HTTP, with a viewer
  page; `cmd/ledsim` runs the daemon on the host with simulated surfaces and the firmware's screen
  patterns, so scenes can be developed in a browser and compared with a unit frame by frame.
- Orion's clock has its own pixel font: square 4×9 digits without serifs, no leading zero, a
  cross-fade when the minute changes. New display mode `clock_date`, the time over the date, is
  orion's default face (profile key `matrix.default_mode`).
- Moments: the app announces what happened (`startup`, `dialog_end`, …) and a per-platform table
  picks the picture — an expression, a scenario or a native animation, with fallbacks; boards and
  `<cfw>/board.conf` override it with `moment.<name>`. Orion ends a dialog with a blink instead of
  the wink, and starts with eyes and a blink.
- Video mode on pixel screens ([docs/video-mode.md](docs/video-mode.md)): `display_mode` `video`
  plays a converted video on orion's screen with its sound, the picture clocked by the audio, then
  returns to the clock; voice, «стоп», replies, timers, calls and music end it. `make video
  VIDEO=<file>` (`cmd/videoconv`, ffmpeg) converts a local video you supply — the motivating one is
  "Bad Apple!!" — into a gzipped `.led` frames file and a WAV, and uploads them to `<cfw>/media/`.
  The LED preview has a **video** button and `ledsim -media`.

**Fixes and operations**
- MQTT publishes are skipped instead of crashing when the daemon runs without a broker.

## v0.8.0 — 2026-10-26

Initial support for orion (Station 3): installed and running on a unit.

**Features**
- Orion: the voice pipeline wakes on the stock «Алиса» model with its second-stage verifier on
  the NPU. spotter-host now reports the verifier's confirmation as the wake (it used to wait for
  a first-stage confirmation that a verified model never sends), no longer treats the 3.21
  library's warning log as an error, and its `probe` no longer crashes on multi-channel models.
- Orion: the screen renders at the stock patterns' own 20 ms frame time instead of the shared
  15 Hz loop, which dropped two of three frames and turned one-frame details (the wink's
  sparkles) into noise. The ring and seg7 displays keep 15 Hz.
- New board-profile key `platform.stop_initd`: vendor init.d scripts whose leftovers outlive the
  gated vendor stack are stopped at startup. Orion stops `S31animation`, whose boot animation
  otherwise loops on the ring forever.
- The egress lock finds an `ip6tables` link in the bundled iptables, which orion's aarch64
  bundle needs to lock IPv6.

**Fixes and operations**
- Orion's CFW root is `/bkupgrade/cfw` (the firmware's own `/bkupgrade` symlink to `/ota`),
  where every hook, the vendor-stack gate and the install kit already looked; the profile said
  `/data/cfw`.
- Install docs for orion: the aarch64 iptables bundle and its `ip6tables` link, no call-mode VQE
  model in this firmware, the example config with wake on. Open: a pixel clock font.

## v0.7.0 — 2026-10-25

**Features**
- Orion (Station 3): the front screen plays the firmware's own screen animations — the voice
  stages (listening, thinking, speaking), the faces (eyes, wink, joy), pause/next/previous, the
  alarm, incoming and outgoing calls, the boot phase and the Wi-Fi setup mode — instead of
  showing none. The Display faces switch and the expressions now exist on orion too. Built
  against the 3.21 firmware dump and not yet checked on glass: if the animations look too dark
  or washed out, set `matrix.gamma` in `<cfw>/board.conf`
  ([docs/display-expressions.md](docs/display-expressions.md), "Screen patterns").
- The front display is driven through a per-display catalogue of scenes and animations: an
  expression resolves to the display's own animation (a clock clip on seg7, a screen pattern on
  a pixel screen), never a converted one. The clock, text and numbers on orion still use the seg7
  picture drawn as pixels until they get pixel drawings of their own.
- HA `display/cmd` `clip` takes semantic names (`listening`) and, on orion, any screen pattern by
  its path (`media/Like`).

**Fixes and operations**
- The intercom's call timer shows on orion's screen too.

## v0.6.0 — 2026-10-25

**Features**
- Intercom, off by default: 1-to-1 calls between speakers in different rooms
  ([docs/intercom.md](docs/intercom.md), HA setup [docs/guides/ha-intercom-setup.md](docs/guides/ha-intercom-setup.md)).
  - «Алиса, позвони в спальню» through an HA script the LLM calls (template in `hass/intercom/`),
    or the HA target select and call button.
  - Answer with «продолжи / играй» or the voice key, decline with «стоп / хватит / пауза» or the
    mute switch; in a call «Алиса, стоп / хватит» hangs up, «Алиса, громче / тише» sets the
    volume — all on the speaker's own spotters, nothing goes to HA.
  - The uplink is the vendor VQE's call mode (neural noise reduction and AGC; `make vqe-models`
    puts its model on the unit); L16 over UDP sealed with AES-GCM; a jitter buffer that also
    absorbs the two speakers' clock drift; signalling over MQTT, signed with a shared secret.
  - The vendor's call sounds and ringing animation, a call timer on the front display, a blue
    ring while the mic streams to the other room.
  - `intercom-peer` (a stand-in second speaker for tests) and `intercom-replay` (tune the uplink
    on a recorded call).

**Fixes and operations**
- spotter-host runs each spotter in its own thread, the VQE loop never waiting for them; it
  switches the VQE's communication mode at runtime and takes `-m <model dir>`.
- `make sounds` also decodes the vendor's call sounds.

**Docs**
- An orion firmware dump sorted locally; QEMU boot rig for bergamot and pickle; rootfs hardening
  research; wishlist: "someone is asleep here" detection.

## v0.5.1 — 2026-10-24

**Docs**
- The README lists the Wi-Fi setup mode.
- Research on making the CFW survive a factory reset: how the reset works per model, pickle's
  replaceable restore package, why bergamot's is not ([docs/research/permanent-install.md](docs/research/permanent-install.md)).

## v0.5.0 — 2026-10-24

**Operations**
- Setup page: "Home settings" for the network-dependent config (broker, health probes, time servers
  and zone, Music Assistant, speaker name, the setup network's own password), checked with the
  daemon's own parser before it is written; the agent restarts with them on leaving setup mode.

## v0.4.0 — 2026-10-24

**Operations**
- Wi-Fi setup mode: hold volume-down + voice + mute for 5 s and the speaker becomes an open access
  point with a page for listing, adding, editing and removing its saved networks (captive portal,
  networks seen nearby). Never persistent: leaving it, the page or a reboot brings the Wi-Fi client
  back. Ships its own static `hostapd` (the firmware's wpa_supplicant has no AP mode).

## v0.3.0 — 2026-10-24

The first tagged release; it covers everything up to now.

**Voice**
- Wake word «Алиса» on the speaker's own front-end and spotter, a Wyoming satellite in HA's
  Assist pipeline; push-to-talk, dialog follow-up, announcements and TTS, HA voice timers, local
  «стоп» / «громче» / «тише».
- TTS jitter buffer: a slow link or a TTS pausing between sentences gives one pause, not a stutter.
- Slow replies: our own reply deadline (HA has none), «стоп» cancels a pending reply, a wake word
  interrupts one without wedging HA's satellite, a wedge watchdog.

**Display and ring**
- The speaker's own clock animations plus our faces; expressions mapped onto the ring and the front
  display; timed scenarios; smooth per-segment transitions.
- Status indication: boot, first connection (and its failure), degraded; no clock digits before
  the first time sync; a red pulse and a spoken error when the voice finds no HA.

**Music**
- A Music Assistant (Sendspin) player (`sendspin-host`): music mixed with the assistant on the
  same speaker, ducking under the assistant, linked volume, the wake word over the music.

**Hardware and HA**
- Board profiles: bergamot (Station Mini 3), pickle (Station Mini 3 Pro), orion (Station 3:
  ring, 21×12 screen, rear ambilight, circular controls; contributed).
- HA MQTT Discovery: ring and ambilight lights, display, volume on the stock curve with a key tick,
  auto-brightness, power self-check, buttons, health; the build version in the device card and a
  Version sensor.

**Operations**
- A crash-loop supervisor with rollback to the previous binary and log rotation; per-platform
  deployment (`make deploy`, `tools/deploy.sh`).
