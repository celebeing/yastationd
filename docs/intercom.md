# Intercom between speakers

Status (2026-10-25): **v1 implemented and tested on the host only** (unit tests, and two daemons
in one process with fake audio talking over loopback UDP); **nothing checked on a speaker yet**.
Off by default (`intercom = false`).

A live two-way voice call from one speaker to another over the home LAN: one room talks to another
hands-free, like a phone call. Scope (decided): **strictly 1-to-1**, plain VoIP-style media, no
group calls, no PBX. How a call is started, answered and ended is **not decided yet** (options
below); v1 ships a neutral control surface for testing that rules none of them out.

The research behind this plan (three passes: audio path/AEC, transport, daemon/HA/UX) is
summarised in the sections below; the decisions are marked **(v1)**, things to settle on a
device **(verify)**.

## Architecture

```
 room A                                                          room B
 mics ─arecord─▶ yastationd ─raw─▶ spotter-host (VQE, AEC vs the     ┌──────────────┐
                    ▲  │◀── vqe_0 16 kHz mono ── hardware playback   │ the same,    │
                    │  │                          reference)         │ mirrored     │
                    │  └─▶ intercom: guard ─▶ RTP/UDP (AES-GCM) ────▶│              │
                    │                                                │              │
 speaker ◀─dmix─ aplay ◀── paced writer ◀── jitter buffer ◀── RTP ◀──│              │
                                                                     └──────────────┘
                 signalling (invite/accept/reject/bye): MQTT, HMAC-signed
```

- **No new processes.** The uplink is the VQE output the daemon already gets from spotter-host
  (`wake.Spotter.SubscribeVQE`): echo-cancelled against the hardware playback reference, which is
  post-dmix, so the far end's voice we play is in it and is cancelled. The reference is in the
  same capture stream as the mics, so it is sample-locked to them (no drift on the AEC side).
- **Without the wake word** (no spotter-host) the uplink falls back to the plain mic
  (`hw.Mic.Subscribe`): no echo cancellation, so the echo guard (below) works harder. Supported,
  but expect echo.
- The downlink plays through its own aplay (a dmix client beside TTS, sendspin and blips), never
  `hw.Player`: Player's Play/Stream/Stop interrupt each other, and a TTS reply or «стоп» must not
  cut the call.

## Media (v1)

- **Codec: L16**, 16 kHz mono, 20 ms frames (320 samples, 640 B). 256 kb/s per direction is
  nothing on a home LAN and costs no CPU. Opus (pure Go) can come later behind a config key if
  weak Wi-Fi needs it; candidates and the armv7 caveat are in the research notes, not pinned yet.
- **Packets: RTP-shaped UDP** (RFC 3550 12-byte header: V=2, PT 96, seq, timestamp in 16 kHz
  units, random SSRC per call; M on the first packet), payload big-endian L16 as RTP wants,
  sealed with **AES-128-GCM** (header as additional data, 16-byte tag). 668 B per packet.
  Port `intercom_port` (default 10710). Datagrams not from the negotiated peer address and SSRC,
  failing authentication, or replayed (64-packet window) are dropped.
- **Jitter buffer:** slots by timestamp; target 40 ms (`intercom_jitter_ms`), hard max 200 ms
  (oldest dropped); late and duplicate packets discarded.
- **Loss concealment:** a missing frame repeats the last one at half gain, then fades to silence
  over the next frames.
- **Clock drift** between the two speakers' independent audio clocks (typically 50–200 ppm, i.e.
  a frame every 100–400 s): corrected in the jitter buffer by its averaged depth — above target +
  10 ms one sample per frame is dropped, below target − 10 ms one is repeated. Corrects up to
  ~3000 ppm with no resampler.
- **Playout paced by the DAC:** a long-running `aplay -t raw -r 48000 -c 1 --buffer-time=60000
  --period-time=20000`, fed by a writer goroutine that pulls one frame from the jitter buffer per
  write. The blocking write is the clock. The pipe is shrunk to one page (`F_SETPIPE_SZ`, 4 KiB)
  and the frames are upsampled 3× to 48 kHz in Go (dmix's own rate), so the pipe holds ~42 ms
  instead of the default 64 KiB ≈ 2 s at 16 kHz — the one big latency trap.
- **Uplink latency:** `wake` reads vqe_0 in 20 ms blocks (it read 64 ms); the intercom keeps at
  most ~100 ms of uplink queued and drops older audio.
- **Budget (mouth to ear):** capture + VQE 40–60 ms, packetisation 20, Wi-Fi 5–30, jitter 40–60,
  playout 40–60: about 150–200 ms. **(verify)** with a clap test.
- **End of media:** 5 s without a packet ends the call (covers a lost `bye` and a peer that died).

## Echo and howling (v1 guard)

Two rooms are acoustically separate, so the only loop is A's residual echo × B's residual echo ×
the gains. The vendor AEC does most of it; the daemon adds cheap safety around it:
- **Fade-in** of both directions over 0.5 s at call start (the AEC has not converged yet).
- **Soft half-duplex:** while the far end is talking and the local uplink level is not clearly
  above the echo-expected level, the uplink is attenuated (−15 dB), released smoothly.
- **Limiters** on both directions; no hard gating (the far end hears quiet, not dropouts).
- **Mic mute** (the hardware latch) keeps the call up; the uplink is silence then.

Later, if needed: a howl detector (sustained narrow-band peak → gain drop or push-to-talk), comfort
noise, and push-to-talk as a config fallback.

**The vendor VQE has a call mode** (phase 3): `YandexVqeC_SetCommunicationMode(vqe, 1)` switches
it at runtime to `regularCallConfig` — GTCRN neural NR fed with the playback reference (residual
echo suppression), AGC to −18 dBFS, limiter, gain ×1 instead of the ASR path's ×50
([vqe-decompile.md](vqe-decompile.md) §3.4). Needs a control channel in spotter-host and device
checks: the mode values (0 ASR / 1 call / 2 baby monitor, inferred), whether the switch loads
`model_call.nnet` (bergamot ships it; **pickle's firmware has no `vqe_models` at all**, a copy
from the user's own bergamot could go to `<cfw>/vqe_models`; orion 3.21 has only `model.nnet`),
and the input scale (maind feeds raw s16 as floats, scale 1).

## Signalling (v1): MQTT

The speakers already share a broker and a topic prefix; no new TCP port, no SIP stack (SIP with
HA's VoIP integration was considered: that is a phone line into Assist, Opus-only, not a bridge
between two endpoints; a full SIP UA adds dialogs/SDP for nothing on a LAN — it can be put in
front of the same media path later if a phone or doorbell must join).

- `<prefix>/<id>/intercom/info` (retained): `{"id","name","port","v":1}`; empty when the feature
  is off. With the existing `<prefix>/<id>/availability` this is the **peer list**: every daemon
  subscribes to `<prefix>/+/intercom/info`.
- `<prefix>/<id>/intercom/sig` (QoS 1, not retained): messages **to** `<id>`:
  `{"t","call","from","ts","nonce","mac", …}` with `t` =
  - `invite` (+ `port`, `ssrc`, `addr`) — the caller's media endpoint;
  - `accept` (+ `port`, `ssrc`, `addr`);
  - `reject` (+ `reason`: `busy`, `declined`, `timeout`, `off`);
  - `bye`.
- **Authentication:** `mac` = HMAC-SHA256 over the message with `intercom_secret` (the same secret
  on every speaker, from the config file like `mqtt_pass`). Rejected: a bad mac, `ts` more than
  30 s off (timesync keeps the clocks right), a nonce seen before.
- **Media key** per call: HMAC-SHA256(secret, "yastation-intercom" ‖ call ‖ caller nonce ‖ callee
  nonce), first 16 bytes; GCM nonce = SSRC ‖ 0 ‖ extended seq.
- Once a call is up, the media no longer depends on the broker.

**No `intercom_secret`, no intercom:** a room mic that any LAN host could open is not an option.

## Call states (v1)

| State | Enters on | Leaves on |
|---|---|---|
| idle | — | call command → calling; valid invite → ringing (or in_call with auto-answer); invite when busy/off → reject |
| calling | our invite sent | accept → in_call; reject → idle; `intercom_ring_secs` (30) → bye, idle |
| ringing | invite received | answer → in_call; caller's bye or ring timeout → idle (missed); hang-up → reject declined |
| in_call | accept sent/received | hang-up (sends bye), bye received, 5 s without media, `intercom_max_secs` (1800), setup mode, daemon stop |

One call at a time; a second invite gets `reject busy`. If both sides invite each other at once,
the invite from the lower device id wins (the other side treats it as its call).

## In the daemon (v1)

- New package `internal/intercom` (no app imports, testable on the host):
  `rtp.go` packet format, `crypt.go` keys/seal/replay window, `jitter.go` buffer + drift + PLC,
  `signal.go` messages and HMAC, `call.go` the state machine with an injected clock and
  transport, `media.go` UDP send/receive, `guard.go` fade/half-duplex/limiter.
- `internal/hw`: a `LiveOut` — a low-latency aplay for a live stream, beside `Player`.
- `internal/app/intercom.go`: wiring — MQTT topics, mic/VQE subscription, playout, ring layer,
  peers for the HA select.
  - **wake word:** stays on during a call, with the far-end guard (see the voice scenario);
    the command spotter's «стоп» does not hang up in v1.
  - **voice key:** ringing → answer; calling/in_call → hang up; it does not start a voice session
    while a call is up.
  - **music:** ducked like during an Assist session (`assistantBusy`).
  - **voice phrases:** the transcript matcher (call / answer / decline / hang up).
  - **HA announcements/TTS** keep playing over the call (dmix mixes them; the far end hears them
    faintly through the AEC'd mic).
  - **setup mode** (the key combo) hangs up first; incoming calls are refused while it runs.
  - **shutdown:** sends bye.
- **Indication** (reworked 2026-10-25 after the first speaker tests; the vendor's own call
  patterns are greens, its front-display "calling" animation is the stock `ringing` clip):

  | State | Ring (`PrioCall` = 45: above Voice, below Error and Setup) | Front display | Sound |
  |---|---|---|---|
  | incoming | green blink, 1 s, until answered | the `ringing` clip, looped | the vendor ringtone `calls_ringing_incoming`, repeated (else the announce chime) |
  | outgoing | green comet | the `ringing` clip | `calls_ringing_outgoing`, repeated |
  | in a call | slow blue breath (no other state's colour); an Assist session's stages show over it | the call's duration `m:ss` | `calls_connected` at the start |
  | ended | a white flash | the clock | `calls_ended` (after a call), `calls_busy` (refused / no answer) |

  The call sounds are vendor files decoded onto the unit by `make sounds` (never in the repo).
- **Keys:** the voice key answers a ringing call and ends any other; the **mic-mute switch
  declines a ringing call and ends a running one** (and incoming calls are refused while muted).
- The daemon no longer opens a listening session by itself while ringing (the ringtone would go
  to STT, and voice control waits for the VQE call mode); «Алиса, ответь» still answers.

## HA control surface (v1, neutral)

Per speaker, by MQTT discovery:

| Entity | What |
|---|---|
| `select` Intercom target | the other speakers by name (the live peer list) |
| `button` Intercom call | calls the selected target; answers when ringing |
| `button` Intercom hang up | hangs up, cancels, or declines |
| `sensor` Intercom | `idle` / `calling` / `ringing` / `in_call`; attributes `peer`, `since` |

Plus a JSON command topic for scripts: `<prefix>/<id>/intercom/set` with
`{"action":"call","to":"<device_id or name>"}`, `{"action":"answer"}`, `{"action":"hangup"}`.

## Config (yastationd.conf)

```
intercom             = false   # the feature; needs intercom_secret
intercom_secret      =         # shared by all speakers (>= 16 characters)
intercom_port        = 10710   # UDP media
intercom_name        =         # the room name others call this speaker by (default device_name)
intercom_auto_answer = false   # true: an incoming call opens at once (after the chime)
intercom_ring_secs   = 30
intercom_max_secs    = 1800
intercom_jitter_ms   = 40
```

## The voice scenario (the user's sketch, 2026-10-25)

```
кабинет:  «Алиса, соедини со спальней»      → «вызываю» (ring: calling)
спальня:  *chime* «вызывают из кабинета, принять?»
          «да»                               → «соединяю» → the call is up
either:   «Алиса, заверши звонок»            → hang up
```

The spotter keeps running during the call. The vendor command spotter has no fitting words
(its 24 are дальше…стоп, хватит, громче, тише, включи/выключи свет — no «да», «заверши», «отбой»),
so every step goes through an ordinary Assist voice session, and **the daemon reads the
transcript HA sends back** (the Wyoming `transcript` event the satellite already gets):

- **Call:** a transcript like «соедини / позвони / свяжи / вызови (меня) с/со/в <room>» where
  `<room>` matches a peer's intercom name (word stems, so «спальней» matches «Спальня») starts a
  call to that peer. The room name is `intercom_name` (default `device_name`).
- **Answer / decline:** while ringing, «да / принять / ответь / алло» answers, «нет / отклони /
  сбрось» declines.
- **Hang up:** in a call, «заверши / положи / сбрось / отбой / закончи (звонок)» hangs up.
- **What HA says** («вызываю», «соединяю», the question): a sentence trigger per phrase in HA
  with a fixed reply keeps the conversation agent (an LLM) out of it — doc example in this
  file once tested. Without one, HA's agent answers whatever it answers; the daemon acts anyway.
- **The question on the callee** («вызывают из кабинета, принять?»): an HA automation on the
  Intercom sensor going to `ringing` (attribute `peer_name`) announces it on that speaker; when
  an announcement ends while ringing, the daemon opens a listening session itself, so a plain
  «да» works without the wake word. Without the automation: the chime, then the same listening
  window. **(verify)** whether `assist_satellite.ask_question` works with our Wyoming satellite
  (it needs start_conversation), which would do the same in one HA call.
- **The wake word during a call:** allowed (that is how «Алиса, заверши звонок» gets in), with a
  guard: a WAKE within ~1.5 s of far-end speech in our playback is ignored (the far room said
  «Алиса», leaking through the AEC). While the Assist session runs, the uplink is muted and the
  far end's voice ducked; the call resumes after it. Today a WAKE during our own playback is
  ignored because the AEC was never measured, so this depends on check 1 below.
- The voice key does the same without speech: answer when ringing, hang up otherwise.

Other options, for later: a key gesture to call a "favourite" room (offline), auto-answer
(`intercom_auto_answer`; the chime and the ring indicator stay mandatory), «стоп»/«хватит» as a
hang-up (false hang-ups from natural speech), a "do not disturb" switch (refuse calls, e.g. at
night), a silent "listen in" mode (baby monitor; the vendor VQE has a preset) — a privacy
decision first.

## Implementation notes (v1)

- Code: `yastationd/internal/intercom` (no daemon imports), `internal/hw/liveout.go`,
  `internal/app/intercom.go`; hooks in `wakeword.go` (the far-end guard), `input.go` (voice key),
  `satellite.go` (transcript; listening after an announcement while ringing), `music.go` (duck),
  `wifisetup.go` (hang up first), `app.go` (bye on shutdown).
- **The jitter target grows only on a late packet** (one that arrived after its slot was
  concealed: real network jitter), one frame at a time up to 160 ms, and decays a frame per 10 s
  without one. Plain loss doesn't grow it — more delay doesn't bring a lost packet back.
- **Drift, simulated:** 30 min at ±300 ppm corrected 8323 / 8318 samples (the expected ~8640),
  the depth stayed at 1–2 frames, no overflow or rebuffering.
- **Loss and jitter, simulated** (10 min, DAC-paced playout): 5 % loss with 0–30 ms delay —
  every packet that arrived was played, no late ones, the target stayed at 40 ms; with 0–80 ms —
  0.3 % late while the target grew, settling at 60 ms.
- The first pop after the prebuffer trims the buffer to the target (the playout may start after
  the network), and a sustained excess of more than two frames skips a frame instead of taking
  seconds of sample-dropping.
- The playout upsamples 16 → 48 kHz by linear interpolation (enough for a voice band ending at
  8 kHz); the far end is ducked to 0.3 while an Assist session or a reply runs in the call, and
  the uplink is silence then.
- Incoming calls are refused while the Wi-Fi setup mode runs (`busy`) or the mic is muted
  (`off`).
- While ringing, the chime plays, and 1.5 s later the daemon opens a listening session unless
  HA has started an announcement meanwhile (then it listens when the announcement ends).
- A speaker that turns the intercom off clears its retained info, but the others keep it listed
  until they restart (the empty retained message doesn't say whose it was); calling it times out.
- `cmd: <prefix>/<id>/intercom/set` takes `{"action":"call","to":…}` with a device id or a room
  name.

**HA side, untested templates** (to be checked and moved into a guide): a sentence trigger per
phrase keeps the conversation agent out and gives the fixed reply —

```yaml
triggers:
  - trigger: conversation
    command: ["соедини (с|со) {room}", "позвони в {room}"]
actions:
  - set_conversation_response: "Вызываю"
```

and the question on the callee — an automation on `sensor.<speaker>_intercom` turning
`ringing` that runs `assist_satellite.announce` on that speaker's satellite with
`"Звонок, {{ state_attr(trigger.entity_id, 'peer_name') }}. Принять?"` (the name is in the
nominative, so «вызывают из кабинета» would need a name map).

## Release gate

No release tag until all of these are done (the user's call, 2026-10-25): the VQE call mode
tried, voice control of calls checked on it, a test on **two speakers**, and the HA integration
with its guide (how to set it up in HA: entities, voice sentences, the callee question).
Commits land on `main` meanwhile; `intercom` stays off by default.

## Phases

1. **v1 on the host (done 2026-10-25):** the `intercom` package with tests (two peers over
   loopback UDP; simulated loss, reordering and ±300 ppm drift; crypto and signalling tables),
   daemon wiring, the voice scenario's transcript matcher, HA entities, config. Off by default;
   not released until checked on speakers.
2. **On two speakers** (needs the user): the checklist below; tune jitter, guard, volume.
3. **VQE call mode** through a spotter-host control channel; pickle's missing call model.
4. **The rest of the UX** (HA sentence triggers and the callee question, tested and written up;
   the other options above); Opus if Wi-Fi needs it;
   push-to-talk fallback; front-display states.

## Findings on a speaker (2026-10-25, bergamot unit A in a room with poor Wi-Fi, a host peer)

- **The call-state indication is not good enough** (the user's verdict): ringing, calling and
  waiting for an answer don't read on the speaker — the green blink alone says nothing about
  "say «да» now". To be redone after the functional tests.
- **An answer said right after the chime is lost:** the listening window opened 3.3 s after the
  call arrived (chime + 1.5 s left for an HA announcement). Fix: listen right after the chime
  and hand the session the audio since the call came in.
- **HA's agent answered the answer** («да» → «Готова помочь…» plus a follow-up question):
  fixed — an intercom transcript cuts HA's reply.
- **The uplink is quiet:** the VQE stream carried speech around −60 dBFS RMS over a −72 floor
  (contaminated sample; measured again with the AGC logging its estimates).
- **Keys and indication, checked by the user (2026-10-25, a host peer):** answer with the voice
  key, decline a ringing call with mute, end a running one with mute, an unanswered call goes
  "missed" after 30 s — all as designed; the ringtone now stops at once (it played on to its
  end after a decline).
- **Wi-Fi from that room:** speaker → host lost 72 and delayed 67 of 2000 packets in 40 s, two
  500 ms gaps, the host's jitter target went up to 160 ms; host → speaker lost 1. The
  speaker's transmit side is the weak one.

## The VQE call mode, first measurements (2026-10-25, bergamot unit A)

The same 82 s call (raw 8-channel capture recorded by the daemon; far-end speech played through
the speaker) replayed on the speaker through `spotter-host vqe` with `-C 0` and `-C 1`:
- **The AEC reference carries our playback** (bergamot ch6: −46…−58 dBFS while the far end
  played, nothing otherwise), and **the ASR-mode AEC already removes the far end's echo** to
  below the room's floor (mics −41…−47 dBFS of echo → vqe_0 −75…−80, vs −71…−73 in silence).
- `-C 1` without a model fails to load `noise_reducer/model.nnet` (that name, not
  `model_call.nnet`) and changes nothing on vqe_0. The firmware on this unit (3.21) ships no
  `vqe_models` at all; with the model from the 0.369 dump under both names in
  `<cfw>/vendor/vqe_models/noise_reducer/` and `spotter-host -m <dir>`, the call mode works.
- **The call-processed audio is the `vqe_gain` channel** (type 9): speech −44 / −19 dBFS
  (median / p95; the vendor AGC targets −18), silence −79…−86 median (neural NR; the ASR-mode
  gain channel sat at −37…−40 there), far-end-only −84. **vqe_0 is bit-identical in both
  modes**, and vqe_bnr changes slightly (max 678 / 32768): one VQE instance can feed the wake
  word and the call at once.

## Testing with one speaker: `intercom-peer`

`yastationd/cmd/intercom-peer` is a stand-in for the second speaker, on any host on the LAN: it
joins the broker as a peer (its own `device_id`, the speakers' `intercom_secret`, in a
yastationd-format config), calls (`-call <device id>`) or auto-answers, records what the speaker
sends (`-record rx.wav`) and sends it a file (`-play`, 16 kHz mono), a tone (`-tone 440`,
`-tone-on 2s` for on/off bursts), an echo of itself (`-echo 300ms`) or silence; every 5 s it logs
the received level and the jitter-buffer counters.

```
cd yastationd && go build -o build/intercom-peer ./cmd/intercom-peer
GOOS=windows GOARCH=amd64 go build -o build/intercom-peer.exe ./cmd/intercom-peer   # WSL2 in NAT mode
```

On WSL2 in NAT mode a speaker can't reach a UDP port inside WSL; the Windows build, started from
WSL, binds on the Windows side (Windows asks once to allow it through its firewall).

What it measures with one speaker: the uplink level of vqe_0 (clipping?), the echo the speaker's
mic picks up of what it plays (a tone in bursts: the received level during the bursts vs between
them), the round trip (`-echo`), and the speaker's Wi-Fi (missing/late packets, the jitter target
it settles on). Checked host to host through the real broker (2026-10-25): call, answer, the tone
arrived at its level, no bad packets.

## To verify on a device

1. The AEC reference carries playback: bergamot ch6, pickle ch3 (orion is verified) — `LEVEL`
   lines while aplay runs; raw vs vqe_0 echo level.
2. Low-latency aplay: the real dmix client buffer (`aplay -v`), no xruns over 30 min; drift
   corrections per minute in the log.
3. Mouth-to-ear latency (a clap recorded on both sides).
4. Echo at the far end at normal and high volume; AEC convergence at call start; howling at max
   volume in two rooms; pickle/orion reference before or after the volume stage (change volume
   mid-call, listen for an echo burst).
5. The uplink level of vqe_0 (ASR gain ×50) — clipping or too quiet for a call.
6. CPU with a call running beside the spotter (bergamot: VQE + «Алиса» + commands ≈ 87 % of one
   core today).
7. The UDP port is reachable between speakers (pickle runs vendor iptables chains at init).
