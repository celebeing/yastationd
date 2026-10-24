# hostapd for the Wi-Fi setup mode

The firmware's `wpa_supplicant` is built without AP mode and the firmware has no `hostapd`, so the
setup mode ([docs/wifi-setup.md](../docs/wifi-setup.md)) brings its own: hostapd 2.11 with
OpenWrt's libnl-tiny, as one static armv7 (musl) binary that runs on every board, like yastationd.

```sh
make            # fetch (pinned: tarball sha256, libnl-tiny commit) and build -> build/hostapd
make clean      # objects; `make distclean` also drops the fetched sources
```

Needs zig 0.14.1 (`ZIG=/path/to/zig` if it's not in PATH), curl, git and make. Build options are in
`hostapd.config`: nl80211 only, internal crypto (enough for WPA2-PSK), no RADIUS, EAP server or
VLANs. `tools/deploy.sh` installs `build/hostapd` as `<cfw>/bin/hostapd` when it exists.

hostapd is © Jouni Malinen and contributors, BSD license; libnl-tiny is LGPL-2.1. Neither source
tree is committed here: `fetch.sh` downloads them into the gitignored `src/`.
