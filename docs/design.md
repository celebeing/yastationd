# Design: the logo, the palette, the web app's look

The rules every screen of the project follows: the web app (`web/`), the Wi-Fi setup portal
(`yastationd/internal/wifisetup/portal.html`), the LED viewer
(`yastationd/internal/frametap/viewer.html`, dark tokens only), the README, and later the
speakers' own screens. A new page or widget takes its colours and patterns from here, not from
scratch.

## The logo

A **deltoid** (the three-cusped hypocycloid: "a triangle with the air let out") stroked in white on
a disc with the **brand gradient**, deep blue `#0A4B9E` (top right) to sky `#2FB8F2` (bottom left).

| File | Use |
|---|---|
| `docs/brand/logo.svg` | the main mark: the README, anything on a neutral background |
| `docs/brand/logo-light.svg` | inverted: a pale disc `#F0FAFE`, the deltoid in the gradient (the sky end darkened to `#28A2E1` to stay visible) |
| `docs/brand/logo-dark.svg` | inverted on a dark disc `#14171C` (the deep end lightened to `#156CB7`) |
| `web/public/favicon.svg` | the favicon and the mark beside "Speakers": a bigger deltoid with a heavier stroke, so it reads at 16 px |

All four come from `docs/brand/make-logo.py` (pure math, no hand-edited paths): the deltoid
`x = 2r cos t + r cos 2t, y = 2r sin t − r sin 2t` with r = 44 on a 512 canvas, a cusp up, its centre
6 px below the disc's (optically centred), stroke 40 with round joins. Change the script, not the SVGs.

**What the logo must not become.** It is a nod to the speakers' own assistant, kept clear of its
marks: Yandex has applied for the Alice logo (a white rounded triangle in a disc) and for violet
(Pantone 2097C) as trademarks in the classes that cover speakers and software. So:

- no violet, purple or magenta anywhere in the brand, and no blue-to-violet gradients;
- the shape stays the deltoid (curved-in sides, sharp cusps), never a rounded straight-sided triangle;
- no «Алиса», «Яндекс» or «Станция» inside the logo; the README uses them only to say what the
  project works with.

A 1-bit version for the speakers' pixel screens (Station 3's 21×12 matrix) is drawn by hand when
needed; the automatic downscale reads poorly below 16 px.

## The palette

Tokens live at the top of `web/src/style.css` (the portal repeats the few it needs). Light is the
base; dark redefines the tokens under `prefers-color-scheme: dark`.

| Token | Light | Dark | For |
|---|---|---|---|
| `--bg` | `#f3f5f8` | `#121417` | the page: a cool grey, not white |
| `--card` | `#ffffff` | `#1b1e22` | cards, the speaker card, inputs |
| `--fg` | `#1a1d22` | `#e8ebef` | text |
| `--muted` | `#6b7380` | `#9aa3ae` | labels, secondary text |
| `--line` | `#dde3ea` | `#2e333a` | hairlines and borders |
| `--accent` | `#0b5cad` | `#38bdf8` | the logo's blue: what you can act on or what is selected (links, primary buttons, switches on, sliders, the selected speaker and section, a changed setting, badges, focus rings) |
| `--on-accent` | `#ffffff` | `#0a1622` | text on an accent fill |
| `--brand` | the logo's gradient | same | the logo only, and at most one decorative stroke per screen |
| `--ok` | `#2f8f4e` | `#4ade80` | healthy: online, pipeline up |
| `--warn` | `#a16207` | `#facc15` | needs a look: degraded, mic off, running short |
| `--on-warn` | `#ffffff` | `#1b1410` | text on a warn fill |
| `--err` | `#b91c1c` | `#f87171` | broken: offline, errors, a failed save |
| `--on-err` | `#ffffff` | `#1b1410` | text on an err fill (the delete button) |

Contrast: the accent is 6.7:1 on white and 7.8:1 on the dark card; `--warn` 4.9:1; text tokens pass
AA. Check a new colour against `--card` and `--bg` in both themes (≥ 4.5:1 for text, ≥ 3:1 for
large text and UI marks) before using it.

**One hue.** The interface takes its one accent from the logo, so the brand and the controls agree;
the warm colours are left to the statuses. (Tried and dropped on the way: an ember logo with an
ember accent read as one long warning, and a teal accent beside it did not match the logo.)

**Meaning, not decoration.** The accent marks interaction and selection only; status uses ok /
warn / err only, and always with words (a chip says "offline", not just red). Neutral data stays
neutral: the vitals' gauges fill in a grey (`--fg` at 45 %) and turn warn or err only past a
threshold, so colour on the card always means "look here". Never use warn or err as a decoration.

## Type

- The system UI stack at 15 px / 1.45; one family. Weight and size carry hierarchy: the speaker's
  name 1.7 rem, section titles 1 rem 600, labels at 0.75–0.8 rem in `--muted`.
- Numbers that change (vitals, volume, levels) use `font-variant-numeric: tabular-nums`, so they
  don't jitter. Addresses are monospace.
- Sentence case everywhere; no all-caps labels.

## Layout patterns

- **The speaker card** (top of every speaker page): identity on the left (name big, address, model
  and version, status chips), the vitals panel beside it (as many columns as fit, a thin gauge under
  each value), the everyday controls in one toolbar row under both. Nothing pushed to a far corner
  with empty space between.
- **Navigation:** groups (Status / Controls / Configuration) with a vertical section menu beside the
  content; pages by topic, never by mechanism ([settings.md](settings.md)).
- **Settings rows:** name, control, share switch on one line, the explaining paragraph under it;
  a changed value gets the accent edge and a Reset; restart-needing changes collect in one floating
  "Apply and restart" bar; rarely touched keys fold under "Advanced" at the bottom of their page.
- Cards: 12–14 px radius, a 1 px `--line` border, no drop shadows (the floating apply bar is the one
  exception: it floats over content).
- Narrow screens (≤ 760 px): every grid stacks into one column; nothing scrolls sideways.

## Motion and accessibility

- Motion only answers an action (a switch, a gauge moving to a new value, an opening fold); nothing
  animates on its own. `prefers-reduced-motion` turns transitions off.
- Every control is reachable by keyboard with a visible focus ring in the accent.
- Icons and colours never carry meaning alone: a label or a tooltip says it.

## Copy

English in the app; plain verbs ("Apply and restart", not "Submit"); say what happens and why in the
paragraph under a setting; units after numbers (`300 ms`, `45 °C`, `91 / 490 MB`).
