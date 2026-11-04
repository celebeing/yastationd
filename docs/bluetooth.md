# Bluetooth audio: the speaker as a phone's Bluetooth speaker

Status (2026-11-04): built; on Station 3 (orion) and Mini 3 Pro (pickle). A phone pairs from the web
app and plays through the speaker over A2DP; the wake word and the assistant duck it.

| Board | State |
|---|---|
| **orion** (Station 3) | On (`audio.bluetooth = true`). The pairing code shows on the pixel screen as `123 456`. |
| **pickle** (Mini 3 Pro) | On. Its four-digit display shows the code in two halves, a second each. |
| **bergamot** (Mini 3) | Off. The firmware runs the same stack, but nothing here was tried on it. |

On-device checks still to do on each board: a pairing with the code, playback, ducking, a reboot
with the phone remembered.

## What the firmware does

A speaker runs the firmware's Bluetooth daemons under the CFW too: `bluetoothd` (BlueZ), `bluealsa`
and `bluealsa-aplay`, which plays whatever a connected phone sends through the `bluetooth` ALSA PCM
(a softvol over the speakers). Two things decide who may pair:

- **The adapter's state.** *Discoverable* means a phone's Bluetooth list shows the speaker.
  *Pairable* means the speaker accepts a pairing request from a phone that knows its address
  (from an earlier scan, or one that paired before). The firmware's `main.conf` has
  `AlwaysPairable = true`, so the speaker boots pairable; it boots not discoverable.
- **The agent.** When a phone asks to pair, BlueZ asks an *agent* program whether to allow it and
  how to check. The firmware runs `just-works-bluez-agent`, which allows every request without any
  check ("Just Works" is Bluetooth's name for pairing with no code). With the vendor stack running,
  the vendor's code kept the speaker non-pairable and paired only in its own setup flow.

So with the vendor stack gated and nothing else, a speaker accepts a pairing from any phone in range
that knows its address, with no code and no one asked, and then plays its audio. Under the CFW
without this feature (bergamot today) that is still so.

## What the CFW does

When the daemon starts (and again whenever `bluetoothd` restarts) it:

1. registers its own agent and makes it the default one, so pairing requests come to it, not to the
   firmware's agent;
2. sets the adapter **not discoverable and not pairable**;
3. sets the adapter's name to `device_name` (what a phone shows in its list).

Its agent accepts only a pairing with a code compared on both sides ("numeric comparison"), and only
inside a pairing window the owner opened. Requests with no code (Just Works) or with a PIN are
refused.

**Off.** `bluetooth = false` (Configuration → Bluetooth audio) switches the speaker's Bluetooth
radio off when the daemon starts: nothing can pair or connect, and the firmware's open pairing is
gone with it. BlueZ keeps the pairings; turning it on again powers the radio and phones reconnect.

**Pairing a phone.** Web app → Configuration → **Bluetooth audio** → **Pair phone**. For two minutes the speaker is
discoverable and pairable. Choose it on the phone: a six-digit code appears on the phone, on the
speaker's display and on the page. If they match, press **Confirm pairing** on the page and accept
on the phone. The phone is then trusted (it reconnects by itself later), and the speaker is hidden
and non-pairable again. The window also closes on **Cancel** or after two minutes. During it only
audio (A2DP) and its remote control (AVRCP: the phone's volume, play/pause) are let through; no
calls (HFP) and no vendor service.

**Forget** removes the phone's pairing, **Disconnect** drops its connection.

**What is left open:** from boot until the daemon starts (seconds), and if `bluetoothd` restarts
while the daemon is down, the firmware's settings apply: pairable, with its agent. A phone already
paired reconnects whenever it likes (that is what pairing is for).

## Sound

`bluealsa-aplay` plays the phone's audio through the `bluetooth` softvol, which the firmware boots
at 225 (−6 dB). The daemon sets it to 255 (0 dB) after it has set the master volume.

**The phone's volume**, `bluetooth_volume` (Configuration → Bluetooth audio, under the pairing):

- `separate` (the default, the firmware's way): BlueALSA scales the phone's audio by the phone's
  own slider, under the speaker's volume. The phone at 100 % plays as loud as the speaker is set,
  never louder; the speaker's keys and HA stay the volume.
- `linked`: the phone's slider is the speaker's volume, as Music Assistant's is with
  `sendspin_volume = linked`. A phone moving its slider sets the speaker's volume (capped by
  `volume_max`); the keys, HA and Music Assistant move the phone's slider. A phone that connects
  gets the speaker's volume on its slider. The phone at 100 % is the speaker at its maximum: on a
  Station 3 that is very loud, so set `volume_max` first.

How: BlueALSA 4 keeps the phone's volume (AVRCP absolute volume) as the `Volume` property of the
phone's PCM (`org.bluealsa.PCM1`, 0..127 per channel). Linked, the daemon turns that PCM's
`SoftVolume` off, so the audio plays unscaled, and follows `Volume` both ways. The setting applies to
phones that connect after it changes.

When the wake word is heard, Bluetooth audio is muted at once (before arbitration, voice ID or HA get
the speaker's own echo), stays muted while the assistant listens and answers, and comes back over
about a second after its 1.5 s hold, like the music ducking ([music-sendspin.md](music-sendspin.md)).
Only the Bluetooth softvol moves; the master never does.

Not done: alarms, intercom calls and Music Assistant music mix with Bluetooth audio rather than
taking its place.

## Implementation

`internal/bluetooth` talks to BlueZ over the system D-Bus (`godbus`): the agent (`Agent1`), the
adapter's properties, the paired devices, and `NameOwnerChanged` to notice a `bluetoothd` restart.
BlueALSA keeps the audio route; the daemon does not move audio itself. The board profile turns it on:

| Key | What |
|---|---|
| `audio.bluetooth` | `true`: the pairing and the web page on this board |
| `audio.bluetooth_volume` | the Bluetooth softvol control, for the ducking and the 0 dB at start |
| `audio.bluetooth_volume_default` | its value at start (255) |

The web API, behind the login:

| Path | Action |
| --- | --- |
| `GET /api/bluetooth` | state and paired devices |
| `POST` / `DELETE /api/bluetooth/pair` | open / cancel the pairing window |
| `POST /api/bluetooth/pair/confirm` | accept the code |
| `POST /api/bluetooth/devices/<id>/disconnect` | disconnect one paired phone |
| `DELETE /api/bluetooth/devices/<id>` | forget one paired phone |

BlueZ keeps pairings in `/data/bluez_data/lib/bluetooth/` ([storage.md](storage.md)); a backup of the
speaker carries them, a factory reset clears them.
