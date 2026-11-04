// Package bleproxy is the radio side of the ESPHome Bluetooth proxy (docs/esphome-api.md) on a
// speaker's BlueZ: raw LE advertisements read off the kernel's HCI monitor channel while
// bluetoothd scans, and GATT connections through bluetoothd's D-Bus API.
package bleproxy

import (
	"encoding/binary"
	"fmt"
	"syscall"
	"time"
	"unsafe"

	"github.com/celebeing/yastationd/yastationd/internal/esphome"
)

// The HCI monitor channel (what btmon reads) copies every HCI packet of every controller to a
// reader, without taking the controller from bluetoothd: the advertising reports of the scan
// bluetoothd runs arrive here as they came from the controller, AD structures and all, which
// bluetoothd's D-Bus API gives only parsed and partly.
const (
	afBluetooth   = 31
	btprotoHCI    = 1
	hciDevNone    = 0xffff
	hciChMonitor  = 2
	monEventPkt   = 3
	hciEvLEMeta   = 0x3e
	leAdvReport   = 0x02
	leExtAdvReprt = 0x0d
)

// monitor reads advertising reports of controller index off the monitor channel and hands them
// to fn, until stop.
func monitor(index uint16, fn func(esphome.Advertisement), stop <-chan struct{}) error {
	fd, err := syscall.Socket(afBluetooth, syscall.SOCK_RAW|syscall.SOCK_CLOEXEC, btprotoHCI)
	if err != nil {
		return fmt.Errorf("hci monitor socket: %w", err)
	}
	defer syscall.Close(fd)
	sa := [6]byte{afBluetooth, 0, byte(hciDevNone & 0xff), byte(hciDevNone >> 8), hciChMonitor, 0}
	if _, _, e := syscall.Syscall(syscall.SYS_BIND, uintptr(fd), uintptr(unsafe.Pointer(&sa[0])), uintptr(len(sa))); e != 0 {
		return fmt.Errorf("hci monitor bind: %w", e)
	}
	tv := syscall.Timeval{Sec: 1}
	syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv)
	buf := make([]byte, 4096)
	for {
		select {
		case <-stop:
			return nil
		default:
		}
		n, err := syscall.Read(fd, buf)
		if err != nil {
			if err == syscall.EAGAIN || err == syscall.EINTR {
				continue
			}
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if n < 6 || binary.LittleEndian.Uint16(buf[0:]) != monEventPkt || binary.LittleEndian.Uint16(buf[2:]) != index {
			continue
		}
		parseEvent(buf[6:n], fn)
	}
}

// parseEvent takes the advertising reports out of an HCI event (one report after another, as the
// kernel reads them).
func parseEvent(p []byte, fn func(esphome.Advertisement)) {
	if len(p) < 4 || p[0] != hciEvLEMeta {
		return
	}
	p = p[2:] // event code, parameter length
	sub, p := p[0], p[1:]
	if len(p) < 1 {
		return
	}
	num, p := int(p[0]), p[1:]
	for i := 0; i < num; i++ {
		var a esphome.Advertisement
		switch sub {
		case leAdvReport: // event type, address type, address, data length, data, RSSI
			if len(p) < 9 {
				return
			}
			at, addr, l := p[1], p[2:8], int(p[8])
			if len(p) < 9+l+1 {
				return
			}
			a = esphome.Advertisement{Address: macNumber(addr), AddressType: uint32(at & 1),
				Data: append([]byte(nil), p[9:9+l]...), RSSI: int32(int8(p[9+l]))}
			p = p[9+l+1:]
		case leExtAdvReprt: // event type(2), address type, address, PHYs, SID, TX power, RSSI, interval(2), direct address type+address, data length, data
			if len(p) < 24 {
				return
			}
			at, addr, rssi, l := p[2], p[3:9], int8(p[13]), int(p[23])
			if len(p) < 24+l {
				return
			}
			if at == 0xff { // anonymous: nothing to address
				p = p[24+l:]
				continue
			}
			a = esphome.Advertisement{Address: macNumber(addr), AddressType: uint32(at & 1),
				Data: append([]byte(nil), p[24:24+l]...), RSSI: int32(rssi)}
			p = p[24+l:]
		default:
			return
		}
		fn(a)
	}
}

// macNumber turns an address as HCI sends it (least significant byte first) into a number.
func macNumber(b []byte) uint64 {
	var v uint64
	for i := 5; i >= 0; i-- {
		v = v<<8 | uint64(b[i])
	}
	return v
}

// macPath is BlueZ's object path of a device: <adapter>/dev_AA_BB_CC_DD_EE_FF.
func macPath(adapter string, addr uint64) string {
	return fmt.Sprintf("%s/dev_%02X_%02X_%02X_%02X_%02X_%02X", adapter,
		byte(addr>>40), byte(addr>>32), byte(addr>>24), byte(addr>>16), byte(addr>>8), byte(addr))
}
