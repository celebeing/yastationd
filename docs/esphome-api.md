# The ESPHome API: the speaker as Home Assistant's Bluetooth proxy

Status (2026-11-04): built; Station 3 (orion) and Mini 3 (bergamot). Off by default: `bluetooth_proxy = true` in
`yastationd.conf` (Configuration → Home integration → Bluetooth proxy). Checked on a unit with Home
Assistant's own client library (aioesphomeapi 46.2.0, the one HA 2026.9 pins): device info, the
advertisements of the room, a GATT connection to a Godox lamp with its services, a disconnection;
and HA's zeroconf found the speaker as an ESPHome device by itself.

## What it is

Home Assistant's Bluetooth stack (habluetooth) uses remote scanners: an ESP32 running ESPHome's
`bluetooth_proxy` forwards the LE advertisements it hears and lets HA connect to devices through
it. The speaker does the same over the same protocol (the ESPHome native API), with its own
Bluetooth radio, so any BLE integration in HA works within range of the speaker: sensors that only
advertise, and devices HA connects to (GATT), such as Godox lamps through `ha-godox-mesh`, which
talks Bluetooth mesh over one GATT connection to a lamp's proxy service.

**Only the proxy.** The API serves no entities, no voice, no logs: everything else stays on MQTT and
Wyoming. The server (`internal/esphome`) is generic, a feature plugs in as a `Component`, so more can
use it later.

## In Home Assistant

- HA discovers it through mDNS (`_esphomelib._tcp`, the instance named `device_id`) and offers to add
  it; or Settings → Devices → Add integration → ESPHome → the speaker's address, port `esphome_port`
  (6053). No encryption key, no password.
- It shows as a device of its own, **linked** to the speaker's MQTT one: the ESPHome entry names its
  device by the Wi-Fi MAC, and with the proxy on, the MQTT discovery's device block carries the same
  MAC in `connections`, so each device's page lists the other under "Linked devices" ("share
  hardware with this device"; HA 2026.9 links devices by a shared connection, it does not fold
  them into one). Name, manufacturer, model and area are the MQTT device's (sent as the ESPHome
  project name); its firmware line reads `<our version> (ESPHome 2026.10.0)`.
- Settings → Bluetooth lists it as a connectable adapter by the speaker's Bluetooth address.

## What the speaker does

| | How | Notes |
|---|---|---|
| The scan | bluetoothd's discovery, LE only, duplicates on (`SetDiscoveryFilter`, `StartDiscovery`), on the proxy's own D-Bus connection | Kept running (re-started if bluetoothd restarted); active scanning. Paused while a phone plays over Bluetooth (`bluetooth_proxy_audio_pause`): the radio is shared |
| Advertisements | the raw advertising reports, read off the kernel's HCI monitor channel (what btmon reads) beside bluetoothd | AD structures as received (bluetoothd's D-Bus API gives them only parsed). Legacy and extended reports; sent in batches of up to 16 every 100 ms |
| A connection | `Device1.Connect` on the device object the scan made, then its GATT objects | Up to `bluetooth_proxy_connections` (3) at once; a client that goes away drops its devices |
| Services | the device's `GattService1/Characteristic1/Descriptor1` objects | BlueZ names them by handle; a characteristic's value handle (what ESP32 proxies report and HA uses) is its declaration's + 1 |
| Read, write, notify | `ReadValue`, `WriteValue` (request or command), `StartNotify` / `StopNotify`, values from `PropertiesChanged` | bluetoothd owns the CCCDs: HA's own CCCD write after a notify request is answered as done |
| MTU | the characteristics' `MTU` | 250 on the lamp tried |

**Feature flags** sent: passive scan, active connections, remote caching (HA keeps the services
between connections; the handles are the device's own, so they stay valid), raw advertisements.
Not: pairing, cache clearing, scanner state and mode (the scan is always active; BlueZ's discovery
has no passive mode).

**The API.** Plaintext frames (`0x00`, length, type, protobuf), API version 1.13, the messages of
`api.proto` that the proxy needs and the handshake (`Hello`, `DeviceInfo`, `ListEntities` → none,
the `Subscribe*` requests HA sends, `Ping`, `Disconnect`). Protobuf is encoded by hand
(`internal/esphome/pb.go`): no generated code, no new dependency.

**Beside the phone audio** ([bluetooth.md](bluetooth.md)): BlueZ does both, A2DP on BR/EDR and LE
central connections. Devices the proxy connects are not paired phones: they fire no
`bluetooth_connected` moment and do not show in the paired list. The pairing agent stays ours: a
device that wants to pair through the proxy is refused (no pairing feature).

## Security

The API is **not encrypted** (ESPHome's Noise encryption is not implemented): anyone on the LAN can
use the proxy — read what the room advertises, and connect to devices through the speaker. The
same as the MQTT broker, which anyone on the LAN can use too. Off by default for that reason.
Encryption (Noise `NNpsk0` with a key in the config) is the next step if the LAN is not trusted.

## Config

```
bluetooth_proxy             = false   # serve HA's Bluetooth proxy (boards with platform.ble_proxy)
bluetooth_proxy_connections = 3       # GATT connections at once
bluetooth_proxy_audio_pause = true    # the scan pauses while a phone plays over Bluetooth
esphome_port                = 6053
```

With `bluetooth = false` (the radio off, [bluetooth.md](bluetooth.md)) the proxy does not start.

## Boards

| Board | |
|---|---|
| orion (Station 3) | On (`platform.ble_proxy = true`): BlueZ 5.70, a Bluetooth 5.0 controller; the LE scan (legacy reports), the monitor channel and a GATT connection tried 2026-11-04 |
| bergamot (Mini 3) | On: the same BlueZ 5.70 and controller (`bluetoothd -E -d`, its debug log goes to `/tmp`); 32 devices in 20 s and a GATT connection to a Godox lamp through it (2026-11-04), the daemon ~2 % CPU. The firmware's pairing agent stays in charge there (no phone pairing feature on this board, [bluetooth.md](bluetooth.md)): a device that asks to pair over a proxy connection is paired without a code |
| pickle (Mini 3 Pro) | Not yet: the same BlueZ stack, untried |

## Not done

- Encryption (above).
- Pairing and bonding through the proxy, cache clearing, the scanner mode setting.
- Pickle.
- A long run beside phone audio: whether the scan, when not paused, disturbs Wi-Fi or audio on the
  shared radio.
