# Wi-Fi setup mode (recovery access point)

Status (2026-10-24): implemented and checked on a bergamot unit:
- the key combo brought it up;
- a phone joined (DHCP), the page edited the saved networks;
- "Connect now" brought the Wi-Fi client back;
- over SSH: the captive DNS/HTTP answered, and after `kill -9` of the daemon in setup mode the
  Wi-Fi client came back.

On pickle, over SSH (no phone): the access point came up, the page's API, DNS and the captive
redirect answered, and the Wi-Fi client was back 10 s after leaving.

## Why

A speaker that can't join its Wi-Fi (the router changed, a password changed, a bad edit) used to
be lost: the only way back was a factory reset and a reinstall. In setup mode the speaker becomes
its own access point and serves a page where the saved networks are listed, added, edited and
removed.

## Using it

1. **Mute the mic, then hold the voice key (Alice) for 5 s.** The same on every board: orion has
   no volume keys and its mute is a switch that can't be held, so the combo is "muted + voice".
   - While you hold, the ring fills with yellow; when it is full, the mode starts.
   - The ring runs a yellow comet.
   - The front display shows wide-open eyes with a dot, `O  O°`.
   - Both stay for as long as the mode lasts, above everything else.
   - A voice press while muted starts no session (it only shows the mute indication), so the
     combo has no side effects; unmute afterwards. Releasing the voice key before 5 s cancels it;
     the mute only has to be on when the voice key goes down (orion's mute flips back by itself
     after ~2 s when held, so holding it through the combo is fine).
2. **Join the open network `yastation-<device_id>`** (`wifi_setup_ssid`) from a phone or laptop.
   - The phone should offer to sign in (captive portal).
   - If it doesn't, open `http://10.42.0.1/`.
3. **Edit the networks.**
   - "Networks around" lists what the speaker saw just before it switched; tap one to fill in its
     name.
   - Saved passwords are never shown: "Edit" with an empty password keeps the current one.
4. **Home settings** (below): the broker, probes, time servers, time zone, Music Assistant, the
   speaker's name.
5. **"Connect now"** leaves setup mode and joins with the saved networks (and restarts the agent if
   the settings changed). "Reboot instead" does the same through a reboot.

The mode also ends by:
- the same key combo again (muted + voice held 5 s);
- a reboot or power cycle: **setup mode is never persistent**, a reboot always comes back as a
  normal Wi-Fi client;
- the daemon stopping.

The speaker is reachable over SSH on `10.42.0.1` while in setup mode, too.

## Home settings

The page's "Home settings" section edits the `yastationd.conf` keys that depend on the network the
speaker lives in:

| Field | Key |
|---|---|
| Speaker name | `device_name` (the device id is shown read-only: changing it makes a new device in HA) |
| MQTT broker | `mqtt_host`, `mqtt_port`, `mqtt_user`, `mqtt_pass` (write-only) |
| Health probes | `probe` (the list) |
| Time | `ntp_server` (the list), `tz` |
| Music Assistant | `sendspin`, `sendspin_server`, `sendspin_name` |
| Setup network password | `wifi_setup_psk` (write-only; "stays open" clears it) |

How an edit is written:
1. Each field is checked: host names, ports, the probe format, a known time zone, a `ws://` URL,
   no line breaks.
2. The whole new file is checked with the daemon's own parser. A config the daemon can't load
   would take setup mode down with it, and the supervisor rolls back only binaries.
3. Only the edited keys' lines change; comments, commented-out examples and other keys stay byte
   for byte. A key that wasn't in the file is appended under `# set from the Wi-Fi setup page`.
4. The write is atomic, with `yastationd.conf.before-setup` kept from the session's first edit.

The daemon loads the new values when setup mode ends: it exits 0 after restoring the Wi-Fi client,
and the supervisor starts it again 1 s later (a deliberate exit, not counted as a crash). There is
no "test the broker" button: in setup mode the speaker has no LAN.

**Not on the page: SSH keys.** `/root/.ssh/authorized_keys` is on the read-only rootfs on both
boards, and a web page doesn't remount the rootfs.

## How it works

The firmware's `wpa_supplicant` (2.9 on bergamot, 2.10 on pickle) is built **without AP mode**: a
`mode=2` network only scans ("AP mode support not included in the build"). There is no
`hostapd`, `udhcpd` or `dnsmasq` either. The vendor's own setup goes over BLE (`ble_initd`, GATT);
the access-point code in `maind`'s `firstrund` is legacy and unused. The Wi-Fi driver (`vlsicomm`,
Amlogic W1) itself supports AP mode on 2.4 GHz (`wpa_cli get_capability modes`; hostapd logs
`device_ap_sme=1`).

**Entering:**
1. Ask the running `wpa_supplicant` for a scan (a full 2.4 + 5 GHz scan takes ~7 s; we wait 10).
2. Remember its exact command line (`/proc/<pid>/cmdline`) in `/tmp/yastationd-wifisetup/` and
   stop it.
3. Start our `hostapd` (`<cfw>/bin/hostapd`, see below): open network (or WPA2 with
   `wifi_setup_psk`), channel 6.
4. Add `10.42.0.1/24` to the interface.
5. Serve, inside yastationd:
   - DHCP: pool .10–.250, the speaker as router and DNS, RFC 8910 captive-portal option;
   - DNS: every A query answers `10.42.0.1`, on that address only; unbound keeps 127.0.0.1;
   - the page on port 80: any other host name is redirected to it, which is what makes phones
     show the portal.

**Leaving:**
1. Stop the servers and hostapd, drop the address.
2. Start `wpa_supplicant` again with the remembered command line.
3. `dhcpcd -n wlan0`.
4. dhcpcd stays running throughout and re-acquires the lease on the carrier change; the address
   came back in ~20 s.

**Nothing about the access point touches persistent storage.** Only the page's edits do:
- wpa_supplicant.conf is written atomically (temp file, fsync, rename);
- the file as it was before the session's first edit is kept as `<conf>.before-setup`;
- network blocks the page didn't change are written back byte for byte.

The conf path is taken from the running wpa_supplicant's `-c` (bergamot
`/bkupgrade/data/wpa_supplicant.conf`, pickle `/data/wpa_supplicant.conf`).

**If the daemon dies in setup mode**, the supervisor restarts it. At startup it finds the marker
and any hostapd of ours still running (by `argv[0]`), stops it and starts wpa_supplicant from the
remembered command line. Verified with `kill -9`: the Wi-Fi client was back in 20 s.

New networks are written as `key_mgmt=WPA-PSK` (or `NONE` for open ones), quoted passphrase or 64
hex digits; SSIDs outside printable ASCII in hex, as wpa_supplicant itself does. WPA3-only (SAE)
networks are not offered as a choice yet.

## Security

The setup network is **open** by default: entering it takes physical access (the key combo), and
it lasts only until left or rebooted.
- Saved passwords never leave the speaker.
- Anyone in range during the session could still add or delete networks.
- `wifi_setup_psk` (8–63 characters) makes it WPA2-PSK.
- `ip_forward` is off, so a client reaches only the speaker. SSH stays key-only.

## Config (yastationd.conf)

```
wifi_setup         = true            # the key combo enters setup mode
wifi_setup_ssid    = yastation-<id>  # default yastation-<device_id>
wifi_setup_psk     =                 # empty = open; 8..63 characters = WPA2-PSK
wifi_setup_channel = 6
wifi_setup_address = 10.42.0.1/24    # must be a /24
hostapd_bin        = <cfw>/bin/hostapd
wpa_conf           =                 # empty = the running wpa_supplicant's -c
```

**Testing over SSH:** `kill -USR1 <yastationd pid>` toggles setup mode like the key combo. Your
SSH session goes down with the Wi-Fi client, so run whatever must happen afterwards (leaving
again, checks, a safety reboot) from a `nohup` script on the device.

## hostapd build

`hostapd/` builds hostapd 2.11 (tarball pinned by sha256) with OpenWrt's libnl-tiny (pinned
commit) as **one static armv7 musl binary** for every board, like yastationd. It needs nothing
from the device. Options in `hostapd/hostapd.config`:
- nl80211;
- internal crypto, enough for WPA2-PSK;
- no RADIUS / EAP server / VLAN.

Build: `make -C hostapd` (zig 0.14.1). About 840 KB stripped. `tools/deploy.sh` rolls it out
when built.

## Boot without a network

Init runs `S40wifi` → `S41dhcpcd` → … → `S52zyastationd` in order:
- `wpa_supplicant -B` returns at once;
- dhcpcd (no `-b`) waits for a lease up to its 30 s timeout and then backgrounds;
- so without a usable network the daemon (and the key combo) comes up ~30 s later, but it comes
  up.

## Boards

- **bergamot:** working (the mute latch is `switch.14`, the voice key `key.582`).
- **pickle:** working (same keys, same driver family; kernel 5.4, the 32-bit hostapd runs fine).
- **orion:** prerequisites present (hostapd deployed, the Wi-Fi chip lists AP mode, same keys);
  not yet tried on a unit. The combo became "muted + voice" for it (2026-10-26).

## Not done

- A setup page in Russian (the UI text follows the repo's English-only rule for now).
- A rescan from the page: our hostapd holds the radio, so the list is the one taken on entry.
- Entering setup mode automatically, e.g. after N minutes without a connection: deliberately
  manual for now.
