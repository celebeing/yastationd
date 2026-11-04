package app

import (
	"context"
	"os"
	"strconv"
	"strings"

	"github.com/celebeing/yastationd/yastationd/internal/bleproxy"
	"github.com/celebeing/yastationd/yastationd/internal/esphome"
	"github.com/celebeing/yastationd/yastationd/internal/version"
)

// The ESPHome native API, served only for the Bluetooth proxy (docs/esphome-api.md): Home
// Assistant connects to it as to an ESP32 proxy. Entities, the voice and everything else stay
// on MQTT and Wyoming.

// esphomeVersion: the ESPHome release whose API this serves (Home Assistant reads it as the
// device's firmware; with a project name set it does not nag about updating it).
const esphomeVersion = "2026.10.0"

// wifiMAC is the speaker's Wi-Fi MAC, upper case ("" when unknown).
func wifiMAC() string {
	b, err := os.ReadFile("/sys/class/net/wlan0/address")
	if err != nil {
		return ""
	}
	return strings.ToUpper(strings.TrimSpace(string(b)))
}

// bluetoothProxyOn: the proxy is configured and possible on this speaker.
func (a *App) bluetoothProxyOn() bool {
	c := a.cfg()
	return c.BluetoothProxy && a.prof.Platform.BLEProxy && (!a.prof.Audio.Bluetooth || c.Bluetooth)
}

func (a *App) startESPHome(ctx context.Context) {
	c := a.cfg()
	if !c.BluetoothProxy {
		return
	}
	switch {
	case !a.prof.Platform.BLEProxy:
		a.log.Warn("bluetooth proxy: not on this board (platform.ble_proxy)")
		return
	case a.prof.Audio.Bluetooth && !c.Bluetooth:
		a.log.Warn("bluetooth proxy: the Bluetooth radio is off (bluetooth = false)")
		return
	}
	proxy := &bleproxy.Proxy{Adapter: "/org/bluez/hci0", Index: 0, Log: a.log,
		Paused: func() bool { return c.BluetoothProxyAudioPause && a.phonePlaying() }}
	go func() {
		if err := proxy.Run(ctx); err != nil {
			a.log.Warn("bluetooth proxy: stopped", "err", err)
		}
	}()
	bt := &esphome.Bluetooth{BLE: proxy, Limit: c.BluetoothProxyConnections, Log: a.log}
	mac := wifiMAC()
	srv := &esphome.Server{Addr: ":" + strconv.Itoa(c.ESPHomePort), Components: []esphome.Component{bt}, Log: a.log,
		Info: func() esphome.DeviceInfo {
			c := a.cfg()
			return esphome.DeviceInfo{
				Name: c.DeviceID, FriendlyName: c.DeviceName, MAC: mac, BluetoothMAC: proxy.Address(),
				Model: a.prof.Model, Manufacturer: "Yandex",
				// The same manufacturer and model as the MQTT device (its card shows them):
				// Home Assistant takes them from the project name's two halves.
				ProjectName:    "Yandex (de-Yandexed)." + a.prof.Model + " (" + a.prof.Name + ")",
				ProjectVersion: version.Version, ESPHomeVersion: esphomeVersion,
				ServerInfo: "yastationd " + version.Version, BluetoothFlags: bt.Flags(),
			}
		}}
	go func() {
		if err := srv.Run(ctx); err != nil {
			a.log.Warn("esphome api: stopped", "err", err)
		}
	}()
	mdns := &esphome.MDNS{Name: c.DeviceID, Port: c.ESPHomePort, Iface: "wlan0", Log: a.log, TXT: map[string]string{
		"friendly_name": c.DeviceName, "version": esphomeVersion, "mac": strings.ToLower(strings.ReplaceAll(mac, ":", "")),
		"platform": "linux", "board": a.prof.Name, "network": "wifi",
		"project_name": "Yandex (de-Yandexed)." + a.prof.Model + " (" + a.prof.Name + ")", "project_version": version.Version,
	}}
	go mdns.Run(ctx)
}

// phonePlaying: a phone plays through the speaker over Bluetooth.
func (a *App) phonePlaying() bool {
	for _, s := range a.mediaSources() {
		if bs, ok := s.(*btSource); ok && bs.playing() {
			return true
		}
	}
	return false
}
