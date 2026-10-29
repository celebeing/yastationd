# Asset packs and themes

Status (2026-10-29): phases 1–3 built and tested on the desk (Go tests), deployed to the dev
units; not yet tried with a real pack on a unit. Still open: a converter for animations
(GIF/PNG → `.led` / `.scled`), ring animations from files, a pack catalogue.

What the owner wants: download extra animations and sounds (an anime pack that swaps every face
for anime emotes; a Half-Life sound pack), apply them in one go as a preset, and save their own.
Packs live in their own repositories and are distributed apart from this one: the CFW repo carries
the format, the loader, the validator and a tool to build packs, never their content (rights
holders).

## The model

- **Moment**: an event in the speaker's life that it reacts to (`internal/app/moments.go`). Every
  reaction the owner may want to change goes through one: code that reacts to an event calls
  `moment()`, never a picture or a sound directly. A moment shows a **show**: a reference (an
  expression, an animation, a scenario) or a sequence of **steps**. A step can light the ring, put
  an animation or text on the front display and play a sound, all at once; steps run one after
  another, and a step that shows nothing is a delay. So any combination of sounds and animations,
  together or in sequence with delays, is one show.
- **Pack**: an archive of assets (sounds, animations per display kind) and, optionally, the moments
  that use them. Installed on a speaker under `<cfw>/packs/<id>/`.
- **Theme stack**: the packs in force, in priority order (`themes`, a shared setting). A moment, a
  sound or an animation is looked up in the stack from the top; the built-in **default theme** is
  always the bottom layer. Applying a pack puts it on top: it overrides what is below it and only
  that (an anime faces pack and a Half-Life sound pack are worn together).
- **The owner's own theme**: the moments editor edits a local pack, `custom`. It is a pack like any
  other and sits in the stack where the owner puts it: a theme above it overrides the owner's
  moments. To start from a theme, import its moments into `custom` and edit from there; export
  `custom` as an archive to keep it or give it away.

Everything the speaker does in reaction to an event is a moment, the status indication included
(booting, HA lost, mute, calls, the timer's alarm, Wi-Fi setup): a theme can change all of it.
What code still draws is a reading, not a reaction: the volume level, a timer's countdown, a
call's duration, the setup gesture's progress, a track title, a video, HA's commands.

## Phase 1: sounds in moments, one hook per event

**Steps play sounds.** `scenarioStep` gains:

- `sound`: a sound name, played beside everything else (mixed through dmix: music and the
  assistant's answers go on); a newer sound in the same slot cuts the older one, other slots'
  sounds and a call's ring tone are not touched.
- `phrase`: a sound name played as speech: it stops other playback first (like the error phrase).

A sound name resolves through the theme stack (`sounds/<name>.wav` of each pack, top first), then
`<cfw>/sounds/<name>.wav` (the decoded stock sounds, `make sounds`), else it is silent. A step with
a sound and no `ms` lasts as long as the sound, so sounds chain one after another; a step with
nothing but `ms` is a pause. A held last step repeats its sound (1.5 s apart) until the moment
ends: the ring tones. A name may list fallbacks, `calls_ringing_incoming|notification_sound`.

**Hooks.** Every event the CFW reacts to is a moment in the catalogue (`momentCatalog`,
`internal/app/defaulttheme.go`), with its slot, its front and ring priorities, its TTL and,
for a call, when its ring yields to the voice stages (ringing: the listening window after
«Алиса»; in a call: that and an Assist session in the call). Code calls `hook(name)` (the voice
stages: `moment()`), and `endHook(name)` for a held one. The default theme (`defaultTheme`, and
`momentDefaults` per display kind) does what the CFW always did, so nothing changes until a theme
says otherwise; events the CFW reacted to with nothing get a moment too, empty by default: the
places other assistants beep.

| Moments | Default |
|---|---|
| `booting`, `boot_failed` (held) | blue / red pulse, the `booting` animation, else a dark front |
| `listening`, `thinking`, `speaking`, `waiting`, `dialog_end`, `arbitration_lost`, `media_pause`, `media_resume`, `startup` | as before ([display-expressions.md](display-expressions.md)); no sound |
| `voice_error`, `voice_muted`, `reply_timeout`, `voice_cancelled`, `wake_busy` | the expressions `error`, `mute`, `sad`, `blink`, `busy` |
| `voice_unavailable` | a bright red pulse and phrase `something_went_wrong` |
| `volume_change` / `volume_edge` | sound `volume_sound` / nothing |
| `announce` | sound `notification_sound`; the announcement waits for its sounds |
| `mic_muted` (held) / `mic_unmuted` | the amber ring / nothing |
| `ha_lost` (held) / `ha_back` | a dim red pulse / nothing |
| `power_problem` | phrase `no_power` |
| `call_outgoing`, `call_incoming`, `call_connected` (held) | green comet, green blink, blue breath; the call animations; ring tones `calls_ringing_*` (incoming else `notification_sound`), `calls_connected` (else the announce chime, as before) |
| `call_ended`, `call_cancelled`, `call_missed`, `call_busy`, `call_failed` | a white flash and `calls_ended`; the flash; nothing; `error` and `calls_busy`; `error` |
| `timer_started` / `timer_finished` (held) | nothing / the amber blink, `ringing` (else a blinking 00:00) and the built-in beep, unless the show has a sound of its own |
| `wifi_setup` (held), `wifi_setup_failed` | the yellow comet and the setup scene; `error` |

Station 3's profile keeps its old silence: `moment.volume_change`, `moment.announce` empty and
`moment.voice_unavailable = ring:unavailable` (orion.conf).

**Regression.** `internal/app/regress_test.go` pins every reaction moved into moments to what the
code before did (slot, priorities, TTL, the effect's colour and period, the clip or its
fallback, the sound and how it plays, the conditions), with no packs and no owner's moments; a
change to the default theme must keep it green or change it on purpose.

**The web app:** a sound or a ring effect as a candidate; in a sequence, a sound (or speech)
beside any step, sound and pause steps, a colour and period for the ring shapes, and a fallback
front for an animation the display lacks. The catalogue lists the sounds this speaker has.

## Phase 2: the theme stack and packs on the speaker

`internal/app/packs.go`.

- `theme` (a list, shared, runtime): pack ids, top first. Empty = the default theme alone.
- `theme_here` (a list, never shared, runtime): this speaker's own stack, worn in place of `theme`
  while it is set (Apply here on the Packs page). Ending it (`[]`) puts the speaker back on
  `theme`. A pack removed from the speaker leaves both lists; an own stack that loses its last
  pack stays as `custom` alone (the default theme), not the shared one.
- `<cfw>/packs/<id>/pack.json` + files. The lookups (moments, sounds, animations) read the stack
  at the point of use; an animation in a pack overrides by its exact name, before the stock
  candidates of a semantic name (`listening` in a pack beats the firmware's `alice_listening`).
- The owner's own pack, `custom`, keeps its moments in `<cfw>/moments.json` (the Moments tab, as
  before) and its sounds and animations under `<cfw>/packs/custom/`. Not named in `theme`, it
  sits just above the default theme, so a speaker with its own moments sees no change; a pack
  applied on top overrides it.
- A pack's animation directories feed the front display's pack layer (`display.Seg7Front.Packs`,
  `PixelFront.PackPatterns`), re-pointed when the theme key changes or a pack is installed.
- A speaker that lacks a pack the shared stack names skips it (the web app says which and offers
  the upload).
- The installed packs are read at start, after an install or a removal through the API, and on
  every `GET /api/packs` when the directory changed (a pack's directory or its `pack.json` time):
  a pack copied into `<cfw>/packs/` by hand or a script shows up when the Packs page is opened.
- `@<pack>/<name>` names one installed pack's animation for this display whether the pack is
  worn or not: the LED page plays a pack's animations with it before it is applied, and a
  moment or a `display/cmd` `clip` may use it too.

## Phase 3: packs in the web app

Assets → Packs (`web/src/packs.tsx`): the theme stack this speaker wears (up/down, take out; a
note says whether the theme is shared, or that this speaker wears its own, with the way back), the
installed packs (what each has for this speaker; Animations = its folder on the LED page; Apply =
on top of the shared theme, "Apply to all" while it is shared; Apply here = on top of this
speaker's own; Copy moments = into `custom` to edit, Remove),
upload a `.zip` (checked on the speaker), save `custom` as a pack archive. Assets → Sounds: every
sound by name and where it comes from, Play (on the speaker), add one (any audio the browser
decodes, converted to WAV there) or remove the owner's. The Moments tab marks a moment a pack
sets, and says when an edit sits under a pack.

The LED page's animation browser (`AnimBrowser` in `web/src/extra.tsx`) walks the animations as
folders: "In force" (the firmware's and the worn theme's, by the names moments use; the stock ones
are paths like `oknix/…`, so subfolders) and one folder per installed pack; a click plays it on
the speaker, a search box finds across all of them. The open folder is in the address
(`#led/@<pack>`), which is how the Packs page links a pack.

API (behind the login): `GET /api/packs` → `{packs: [{id, name, version, author, source,
license, displays, moments, sounds, anims, fits, bytes, position}], stack, here, shared_stack,
shared, display, missing, free, max_bytes}` (`stack`: what this speaker wears; `here`: that is
its own `theme_here`; `shared`: the theme setting is shared); `PUT /api/packs` (the zip, ≤ 32 MB,
≤ 64 MB unpacked); `PUT /api/packs/stack {"stack": [ids], "here": bool}`; `POST
/api/packs/<id>/apply[?here=1]`, `/import`; `DELETE
/api/packs/<id>`; `GET /api/packs/custom/export?id=&name=`; `PUT|DELETE
/api/packs/custom/sounds|anims/<name>`.

## The pack format

A zip with `pack.json` at its root:

```json
{
  "id": "anime-faces",            // a-z0-9 and -, up to 40
  "name": "Anime faces",
  "version": "1.0.0",
  "author": "…", "source": "…", "license": "…",
  "displays": ["pixels-21x12", "seg7"],
  "moments": {                    // optional; by display kind, "any" for every one
    "any":    { "listening": [ { "steps": [ { "express": "listening", "sound": "wake" } ] } ] },
    "pixels-21x12": { "dialog_end": [ "happy" ] }
  }
}
```

Files: `sounds/<name>.wav` (PCM WAV: aplay plays it through dmix), `anims/pixels-21x12/<name>.led`
(screen patterns, `display.LoadPattern`'s format), `anims/seg7/<name>.scled`. Everything is
parsed by the daemon's own loaders on upload; a file that fails, a name outside `[a-z0-9_-]`, a
path outside the pack or an archive over the size limit refuses the pack. The pack's own names
should be prefixed (`hl_`, `anime_`) where they are new, so two packs don't collide; a name that
matches a stock one replaces it on purpose.

`cmd/packtool` builds and checks a pack on the host with the speaker's own checks: `make pack
PACK=<dir>` zips a pack directory (dot files left out) and checks it; `make pack PACK=x.zip`
checks an archive. To come: GIF/PNG frames → `.led` / `.scled`, mp3/ogg → WAV.

## Open

- Ring animations from files (keyframes per pixel): the ring is procedural today; a pack can only
  pick effects and colours.
- A catalogue of packs fetched by the browser (a JSON index on a host with CORS; GitHub Releases
  assets have none): later, manual upload first.
