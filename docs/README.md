# The docs: how they are laid out

Four kinds of documents, kept apart so that each reader loads only what they need:

| Where | What | Who reads it |
|---|---|---|
| `docs/*.md` | **Specifications**: what the CFW does and how it is built, as it is now; plus the developer guides (the [architecture](architecture.md), [adding a feature](adding-a-feature.md), [porting a speaker](porting-a-speaker.md), [building](building.md), [releasing](releasing.md)). Listed in [index.md](index.md) | anyone changing the code; agents read the index before planning |
| [`docs/guides/`](guides/README.md) | **User guides**: how to install and update the CFW, connect it to Home Assistant, send diagnostics. Steps, not internals | owners, and agents acting for one |
| [`docs/research/`](research/README.md) | **Research**: decompiles of the firmware, the hardware teardown, measurements, the original design and the alternatives we chose against. Evidence and reasoning, possibly outdated where the code went another way | whoever needs the *why* or is about to reopen a decision |
| [`docs/boards/`](boards/README.md) | **The boards**: the supported models and one doc per board (hardware facts, what was verified on it) | porting, and anything board-specific |

Also here: `brand/` (the logo's sources), `media/` (pictures the docs and the README use).

## What goes where

- **A spec describes what is built** and changes with the code, in the same commit. A planned
  feature gets a spec when it is built; until then it is an item in [wishlist.md](wishlist.md).
- **A guide is steps for a user**: what to install, what to click, what to put in HA. It links the
  spec for how things work instead of explaining them.
- **Research is evidence**: what the firmware does, what was measured, which options were weighed.
  A spec cites it instead of repeating it. Research is not kept in sync with the code; a finding
  that turned out wrong gets a correction note, not a silent rewrite.
- **Splitting a doc** (a spec and its research, a spec and its guide): the halves link each other.
- **Mention another doc as a link**, `[deploying.md](guides/deploying.md)`, not as `code`: a reader
  should be one click away. Never link a gitignored `*.local.md` file (it is a broken link for
  everyone else). `tools/build.sh docs-check` checks every relative link.
- **Each folder has its index** (its `README.md`); [index.md](index.md) lists only the specs and points
  to the others, so reading it does not load everything.
- Not for the repo: anything private (addresses, names, real rooms) goes to gitignored
  `*.local.md` files; dumps stay in `dumps/`.
