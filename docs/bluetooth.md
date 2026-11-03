# Bluetooth audio pairing

`yastationd` owns Bluetooth audio pairing through the existing BlueZ and BlueALSA services. It never
starts the vendor assistant or its `ble_initd` Wi-Fi/token GATT service. The feature is available in
the authenticated web app only when the running speaker exposes a usable BlueZ adapter.

## Pairing a phone

Open **Bluetooth audio**, select **Pair phone**, then choose the speaker on the phone within two
minutes. BlueZ supplies a zero-padded six-digit numeric-comparison code. It appears on the speaker
and in the logged-in page; compare it with the phone, then select **Confirm pairing**. The pairing
window ends after success, cancellation or two minutes. Bluetooth is otherwise neither discoverable
nor pairable.

The phone sees the configured `device_name` in its Bluetooth picker. Changing that name restarts
`yastationd`, which updates BlueZ's adapter alias before the speaker next becomes discoverable.

Station 3's pixel display draws the code as two groups of three. The Mini 3 and Mini 3 Pro have only
four seven-segment digits, so they alternate the first and second three-digit groups once a second.
They never fall back to an unverified code-less pairing method.

Only numeric comparison is accepted. Legacy PIN and Just Works requests are rejected. A paired phone
is trusted only after the owner confirms the matching code. The initial scope authorizes A2DP sink
audio only; it does not expose HFP or a vendor GATT service. **Forget** removes BlueZ's pairing record.
On Orion, firmware starts that softvol at 225 (−6 dB). `yastationd` restores it to 255 (0 dB) at
startup; normal speaker volume remains the only owner-visible volume control.

When a wake word arrives, Orion Bluetooth music mutes immediately—before arbitration, voice ID or
HA can receive its echo. The mute bridge lasts two seconds while the normal assistant session
starts. It remains muted through listening, the assistant's reply and the 1.5-second follow-up hold,
fades back in about one second. This adjusts only the board-declared Bluetooth softvol; it never
raises `Master Playback Volume` or bypasses the owner's `volume_max`.

## Implementation

`internal/bluetooth` is a native Go system-D-Bus client. It registers the BlueZ `Agent1` as the default
agent and rejects requests outside its bounded pairing session. It watches the pairing result, marks
the confirmed device trusted and returns the adapter to hidden/non-pairable state. BlueALSA continues
to own the audio route; `yastationd` does not route audio itself.

The web API is authenticated:

| Path | Action |
| --- | --- |
| `GET /api/bluetooth` | State and paired devices |
| `POST` / `DELETE /api/bluetooth/pair` | Start / cancel the pairing window |
| `POST /api/bluetooth/pair/confirm` | Accept the numeric comparison |
| `POST /api/bluetooth/devices/<id>/disconnect` | Disconnect one paired phone |
| `DELETE /api/bluetooth/devices/<id>` | Forget one paired phone |

## Validation boundary

The Station 3 controller and BlueALSA route were surveyed before this feature. The runtime capability
probe keeps the page hidden where BlueZ cannot be reached. Pairing, numeric confirmation, A2DP playback,
reboot persistence, and the Mini display fallback still require an on-device check on each supported
model before claiming model-level verification.
