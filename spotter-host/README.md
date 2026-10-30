# spotter-host — Path A: the vendor Yandex spotter without `maind`

A small armhf C program that links the stock `libyandex_evil.so` and drives two of its exported C APIs directly:
the **spotter** (wake word + commands) and the **VQE** mic-array front-end. It
feeds the library PCM and prints wake events. This is "Path A" from [`docs/research/spotter-decompile.md`](../docs/research/spotter-decompile.md) §8:
we get the real «Алиса» spotter with no reimplementation, but the proprietary blob stays in the loop.

Status (2026-10-22): **runs natively on the device** (from `/tmp/spotter/`), including the vendor VQE on live
`hw:0,1` input and the vendor command spotter (стоп/хватит/громче/тише/пауза/…). Nothing starts `maind`.
Real-voice recall is **not measured yet**. See "On-device results" below and
[`docs/research/voice-phase2-plan.md`](../docs/research/voice-phase2-plan.md) for the channel map, the VQE verdict and the yastationd plan.

**Multi-model:** the capture layout and VQE preset are flags — `-P <preset>` (default `bergamot`),
`-N <channels>` (8), `-M <mic channels>` (`0,1,2`), `-R <speaker-feedback channel>` (6, `-1` = none);
presets with several speaker references (orion: 7) take them all as `-Q <ref channels>` in VQE order.
One source links against both the 0.369 library (bergamot, pickle) and the 3.21 one (orion), which
dropped the `spotter_error_*` helpers: they are weak references, and without them an error prints
the library's warning buffer that 3.21 leaves in the error slot.
yastationd passes them from its board profile; see [`../docs/boards/README.md`](../docs/boards/README.md).

## Files

| File | What |
|---|---|
| `spotter_api.h` | The reconstructed spotter C API: signatures, the event enum and the channel types. Built from shim disassembly and confirmed by runs |
| `vqe_api.h` | The reconstructed VQE C API (`YandexVqeC_*`), from disassembly of the exported wrappers. Confirmed by device runs except where marked (?) |
| `spotter_host.c` | The host. `probe` = model channel requirements; `run` = PCM on stdin to events; `vqe` = raw `hw:0,1` capture through the VQE to 16 kHz PCM; `listen` = raw `hw:0,1` capture, then VQE, then wake + command spotters, then event lines |

`-m DIR` points the VQE at its model directory (default `/system/vendor/quasar/vqe_models`); the call mode (`-C 1`) loads `DIR/noise_reducer/model.nnet`, which firmware 3.21 doesn't ship (docs/intercom.md, "The VQE call mode").
| `voiceprint.c` | `voice <bio_lingware>`: the firmware's voice biometry for voice ID — fbank, the ECAPA `body`/`head` through the vendor's `libtflite.so` (`dlopen`, its own op resolver), commands on stdin, `VOICE` lines on stdout ([docs/voice-id.md](../docs/voice-id.md)) |
| `build.sh` | Cross-build: `ARCH=armhf` (bergamot, links the **device's** glibc 2.30, see below) or `ARCH=aarch64` (pickle; plain dynamic link, RPATH so the lib's `libtflite.so` resolves) |
| `tools/listen.sh` | The on-device live test (wraps `arecord` + `listen`, adds wall-clock timestamps) |
| `tools/hwfree.sh` | Checks that `hw:0,1` is free: no `arecord` running, and `yastationd` doesn't hold `pcmC0D1c` |
| `tools/interleave.py` | Turns mono S16 into N-channel interleaved PCM for tests (needs numpy) |

## API facts pinned down here (in addition to the decompile doc)

- `spotter_create(conf, sample_rate, opt, err)`: `sample_rate` must be a multiple of the native 16000.
  - Evidence: the check `rate % conf->native_rate` at vaddr 0x483798 and the string "Sample rate given to spotter not a multiple of its native sample rate".
  - `opt` is an optional pointer whose purpose is unknown; NULL works.
- `spotter_create_multichannel(conf, sample_rate, n, const int *chan_types, opt, err)`: one int per interleaved channel.
- `spotter_conf_get_required_channel_count(conf, chan_type, err)` returns how many channels **of that type** the model needs.
- Channel types run 0..5. Values 6 and up give "Unknown channel type at index N".
  - Confirmed by runs: 0 = omni (`vqe_0`), 3 = BNR (`vqe_bnr`), 4 = raw mic (the freq-filter input).
  - 1, 2 and 5 are probably beamforming, feedback and extra raw mics; this is inferred from the order of fields in the channel-descriptor struct.
- `spotter_error_free(spotter_error **slot)` takes the **slot**, not the object. Passing the object segfaults.
- Required layouts:

  | Model | Layout |
  |---|---|
  | active bergamot «Алиса» (`8cf52356…`, x3-bnr) | `0,3,4` |
  | built-in fallback «Алиса» (`04Mar22`) | `0,4` |
  | built-in «Яндекс» | `0,4` |

  Plain `spotter_create` (layout `{0}`) is refused for all three models.

## Test results (qemu, synthetic espeak-ng TTS)

The test track is 30 s of espeak-ng `ru` at two speaking rates with a noise floor of about 3 LSB. It contains 6× «Алиса», 2× «Яндекс», 2× a neutral phrase and 1.5 s gaps.
The same mono signal goes into every channel unless noted. A "hit" is `CONFIRMATION_1ST_STAGE`.

| Model / input | «Алиса» hits | False hits on other phrases |
|---|---|---|
| bergamot «Алиса», `0,3,4`, signal near full scale | **6/6** | 0 |
| same at −10, −20, −30, −40 dB | 6/6 at every level | 0 |
| same, raw-mic channel zeroed | 6/6 | 0 |
| same, **BNR channel zeroed** | **2/6** | 0 |
| −30 dB, omni only (BNR and raw zeroed) | 0/6 | 0 |
| built-in «Алиса», `0,4` | 6/6 | 0 (one SUBHIT only) |
| built-in «Яндекс», `0,4` | 1/2 «Яндекс» (plus one SUBHIT) | 0 |

What this suggests:
- **The bergamot model needs a real signal on the BNR input.** Feeding the same AEC'd stream into both omni and BNR works. Leaving BNR silent breaks it, so the two-channel model really does fuse its inputs. This confirms option (a) in [docs/research/spotter-decompile.md](../docs/research/spotter-decompile.md) §7.3.
- **The raw-mic channel does not affect hits.** It only feeds the freq-filter, which can reject a hit but never cause one. It must be present in the layout, but can be zeros or a raw mic.
- **Absolute level barely matters**, at least across 0 to −40 dB with SNR held constant. That softens the "level is the main knob" worry from the decompile doc. Real far-field SNR is a separate question.
- Caveat: this is TTS, not real speech, and there are only 30 s of negatives. It says nothing about false accepts per hour or far-field recall. Those need real recordings (§7.3 protocol).
- Cost under qemu was about 31 s of CPU for 30 s of audio and about 21 MB max RSS including qemu. Natively on the device's Cortex-A it should be well under real time.

## On-device results (2026-10-22)

The device has 4× Cortex-A53 at 1.2 GHz and 244 MB RAM. The host is about 23 KB and runs from `/tmp/spotter/` with
`LD_LIBRARY_PATH=/system/vendor/quasar/libs`, using the device's own libs and models. It never touches `/bkupgrade/cfw`.

### `probe`, natively

Every model loads. Required layouts are as follows. "Plain" means `spotter_create` with layout `{0}`.

| Model (on-device path) | What | Layout |
|---|---|---|
| `/data/quasar/data/spotter_model/8cf52356-…` | active «Алиса» `bergamot-alisa-13Nov-x3-bnr-minispeakers-29Jan24` | `0,3,4` |
| `/data/quasar/data/spotter_model/a23e7ada-…` (= `spotter_models/command`) | **command spotter** `quasar-commands-10Feb25…`, 27 outputs | `0` (plain works) |
| `/data/quasar/data/spotter_model/ef5534dc-…` | intonation-interruption | `0` |
| `/data/quasar/data/spotter_model/f36fe333-…` / `d918817b-…` | VAD `<speech_started>/<speech_finished>` | `0` |
| `spotter_models/activation/{alisa,yandex,yasmina}` | built-in wake words | `0,4` |
| `spotter_models/spotterRecognizer` | the same command set as a `model.tflite` ("NPU" variant) | `0` |

The active models are listed in `/data/quasar/data/spotter.json`. The `multiflags.txt` "could not read" INFO line is harmless.

### Cost (live `hw:0,1`, % of **one** core, from `/proc/<pid>/stat`, 25 s windows)

| Pipeline | CPU | RSS |
|---|---|---|
| VQE only (`vqe`) | 42 % | 13 MB |
| no VQE, «Алиса» at 48 kHz (`listen -x 0,0,1 -a`) | 40 % | 14.5 MB |
| VQE + «Алиса» (`listen -a`) | 73 % | 18 MB |
| VQE + «Алиса» + commands (`listen -a -k`) | 87 % (90.5 % over a 12-min run) | 20 MB |

That is about 22 % of the whole SoC for the full pipeline. The device stays usable: load average stayed under 1, and `yastationd` was unaffected.

### Live stability (quiet room, nobody speaking)

- A 12-minute run of VQE + «Алиса» + commands produced **0 events**: no hits, no SUBHITs, no errors, and RSS stayed flat at 20 MB.
- The room was very quiet: raw mic about −61 dBFS, `vqe_0` about −79 dBFS. That makes this weak evidence about false accepts.
- **The ALSA buffer matters.** With arecord's default `hw:0,1` buffer (2048 frames, about 43 ms) the run logged **853 overruns in 12 min**,
  because the spotter processes in ~60 ms batches. `--buffer-time=1000000 --period-time=20000` gave **0 overruns** in 60 s.
  `tools/listen.sh` uses that setting. Anything else reading `hw:0,1` through a pipe needs the same.

### Synthetic end-to-end check (on device)

The input is 68 s of espeak-ng TTS mixed with **real room noise captured on the device**, laid out as a fake 8-channel `hw:0,1` stream. The same speech is on
ch0–2, with ch6 = −1 as in an idle capture. It contains 6× «Алиса» (2 alone, 2 «…включи свет», 2 «…стоп») and 20 command utterances. There are also negatives: «сегодня хорошая погода» and «Яндекс».
This is not real speech; it only checks the plumbing.

| Front-end into the «Алиса» model (`0,3,4`) | −30 dBFS speech | −45 dBFS speech |
|---|---|---|
| VQE, BNR input ← `vqe_bnr` (default) | 1/6 (+2 SUBHIT) | 1/6 (+2 SUBHIT) |
| VQE, BNR input ← `vqe_0` (`-b`) | **6/6** | **6/6** |
| no VQE, mic0/mic0/mic1 at 48 kHz (`-x 0,0,1`) | **6/6** | **6/6** |

The `vqe_bnr` failure is a **test artefact**. The 2-reference BNR is an adaptive canceller: speech that is bit-identical on all three
mics looks like coherent "noise" to it, so it converges and removes it. The `vqe_bnr` level falls from −37 to −74 dBFS during
speech over the track. Real speech differs between mics, so `vqe_bnr` must be re-checked with a real voice (see the A/B in `listen.sh`).

The command spotter is plain `0`. "Hits" counts confirmations out of 20 utterances:
- −30 dBFS: vqe_0 gave 8/20 and raw mic0 at 48 k gave 10/20.
- −45 dBFS: vqe_0 gave 3/20, raw gave 1/20, and vqe_gain (`-g`, ×50) gave 7/20. At −30 dBFS, `-g` clips and drops to 2/20.
- The model recognised стоп, хватит, громче, пауза and следующий, but never тише, дальше or включи/выключи свет in TTS.

The command model is clearly **level-sensitive**; the activation model is not. There were **no false hits** in any run, from either model.

### The live test the user runs (say «Алиса» / «стоп» / «громче» at 1, 3 and 5 m)

```sh
# dev box: stage (no scp on the device)
D="ssh -i <device-ssh-key> -o IdentitiesOnly=yes root@<speaker-ip>"
$D 'mkdir -p /tmp/spotter'
$D 'cat > /tmp/spotter/spotter-host; chmod +x /tmp/spotter/spotter-host' < spotter-host   # build.sh output
$D 'cat > /tmp/spotter/listen.sh'  < spotter-host/tools/listen.sh
$D 'cat > /tmp/spotter/hwfree.sh; chmod +x /tmp/spotter/hwfree.sh' < spotter-host/tools/hwfree.sh
# on the device (refuses if yastationd is capturing): 10 minutes
sh /tmp/spotter/listen.sh 600 | tee /tmp/spotter/live.log
# A/B variants:  sh /tmp/spotter/listen.sh 600 -b    (BNR <- vqe_0)
#                sh /tmp/spotter/listen.sh 600 -x 0,0,1   (no VQE)
```

Log format: `<HH:MM:SS> <stream_seconds> WAKE|CMD <EVENT> [phrase]`. A wake is `WAKE CONFIRMATION_1ST_STAGE алиса`.
`SUBHIT` means "almost". The `LEVEL` line each second carries `vqe bnr gain raw spk` in dBFS plus the omni mic index. While the speaker is playing,
`spk` (the HW feedback) should rise above −90; if it does not, ch6 is not the reference (see the plan doc).

## Sandbox (how this was run)

This is vendor code, so nothing runs on the host directly:

- `qemu-arm-static -L <sysroot>` with a sysroot copied from the dump. It holds the device `/lib` (glibc 2.30) and the **intact** `libyandex_evil.so` from `dumps/rootfs-full.tar`. The copy under `dumps/rootfs/` is truncated (4,587,520 B instead of 4,912,400 B).
- `unshare -rn` gives a user and network namespace, so there is **no network** at all.
- A `systemd-run --user --scope -p MemoryMax=3G -p MemorySwapMax=0` cgroup plus `nice 19` / `ionice -c3`.
- Model directories are copied in read-only.

The sysroot, the wrapper scripts (`sbx.sh`, `sbx-gdb.sh` for a gdb-multiarch backtrace via qemu's gdbstub) and the test audio are in the session scratchpad, not in the repo. To recreate the sysroot:

```
sysroot/lib/                            <- dumps/rootfs/lib/*.so*
sysroot/system/vendor/quasar/libs/      <- libyandex_evil.so + libtflite.so (from rootfs-full.tar), libiconv.so
sysroot/models/<uuid>/                  <- dumps/rootfs/data/quasar/data/spotter_model/*
sysroot/models/builtin/                 <- dumps/rootfs/system/vendor/quasar/spotter_models
```

Then:

```sh
SYSROOT=.../sysroot OUT=.../spotter-host ./build.sh
unshare -rn qemu-arm-static -L .../sysroot -E LD_LIBRARY_PATH=/system/vendor/quasar/libs \
  .../spotter-host run -t 0,3,4 /models/8cf52356-a4f7-4ef5-9100-e95067abdc0a < pcm3ch.raw
```

### Why `build.sh` links by hand

The Ubuntu cross toolchain ships glibc 2.35, which binds `__libc_start_main@GLIBC_2.34`. The device has 2.30. `build.sh` therefore links with
`-nostdlib -no-pie` straight against the device's `libc.so.6`, `libgcc_s.so.1` and `ld-linux-armhf.so.3`, which leaves only `GLIBC_2.4`
symbol versions. A side effect is that the 2.35 `crt1.o` passes no init function, so **constructors in the host executable itself do not run**. Plain C
is fine with that. Constructors in the shared libraries are run by ld.so as usual.

## Next steps (2026-10-22; since then)

Item 3 is done (`serve` is what the daemon runs), item 1 in part: real-voice tests ran in the
default mode only (bergamot «Алиса» 10/10 at 1 and 3 m, orion 14/14 in a room); `-b` and `-x`
were not compared on real voice. Item 2 is answered by the intercom's tests (ch6 carries our
playback and the AEC takes its echo below the room's floor: [docs/intercom.md](../docs/intercom.md),
the call-mode results); whether the spotter then works over our own playback is not tried. Item 4
is open. What is built and still open:
[docs/wake-word.md](../docs/wake-word.md). The list as it was, from
[`docs/research/voice-phase2-plan.md`](../docs/research/voice-phase2-plan.md):
1. Run the user's real-voice test with `listen.sh`, comparing default, `-b` and `-x 0,0,1`.
2. Confirm that ch6 carries playback, and that the VQE's AEC removes it, by playing something during `listen`.
3. Add a `serve` mode for `yastationd`: raw 8-channel PCM on stdin, event lines on stdout, and optionally `vqe_0` on fd 3 for the STT stream.
4. Run an FA/h soak on household audio, and tune the threshold through a copy of `flags.txt` if needed.
