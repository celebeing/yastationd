package hw

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// Linux input constants used by the Station.
const (
	EvSyn           = 0x00
	EvKey           = 0x01
	EvAbs           = 0x03
	EvSw            = 0x05
	KeyMute         = 113
	KeyVolumeDown   = 114
	KeyVolumeUp     = 115
	KeyVoiceCommand = 582

	SwMuteDevice = 0x0e
)

// InputEvent is one evdev event (struct input_event without its timestamp).
type InputEvent struct {
	Type  uint16
	Code  uint16
	Value int32
}

// parseRecord decodes one input_event; type/code/value are always the last 8 bytes, whatever
// the size of the timeval in front of them.
func parseRecord(b []byte) InputEvent {
	off := len(b) - 8
	return InputEvent{
		Type:  binary.LittleEndian.Uint16(b[off:]),
		Code:  binary.LittleEndian.Uint16(b[off+2:]),
		Value: int32(binary.LittleEndian.Uint32(b[off+4:])),
	}
}

// ReadEvents reads r until it fails, sending every non-SYN event to fn.
//
// The record size is learned from the first read. The kernel only returns whole records, so a
// read into a 24-byte buffer yields 16 bytes for the compat (32-bit timeval) layout and 24 for
// the 64-bit one. The userland is 32-bit on a 64-bit kernel, so either can show up.
func ReadEvents(r io.Reader, fn func(InputEvent)) error {
	first := make([]byte, 24)
	n, err := r.Read(first)
	if err != nil {
		return err
	}
	size := n
	if size != 16 && size != 24 {
		return fmt.Errorf("unexpected input_event size %d", n)
	}
	emit := func(rec []byte) {
		if ev := parseRecord(rec); ev.Type != EvSyn {
			fn(ev)
		}
	}
	emit(first[:size])
	buf := make([]byte, size*32)
	for {
		n, err := r.Read(buf)
		for off := 0; off+size <= n; off += size {
			emit(buf[off : off+size])
		}
		if err != nil {
			return err
		}
	}
}

// OpenInput opens an evdev device for ReadEvents.
func OpenInput(path string) (*os.File, error) { return os.Open(path) }

// FindInput maps an evdev device's kernel name (the N: Name= line of /proc/bus/input/devices,
// under root) to its /dev/input/eventN path, so profiles needn't rely on probe order.
func FindInput(root, name string) (string, error) {
	path, _, err := FindInputInfo(root, name)
	return path, err
}

// FindInputInfo is FindInput plus whether the device reports switches (a non-zero B: SW=
// bitmap) — only such a device may be asked for its switch state.
func FindInputInfo(root, name string) (path string, hasSwitch bool, err error) {
	b, err := os.ReadFile(root + "/proc/bus/input/devices")
	if err != nil {
		return "", false, err
	}
	for _, blk := range strings.Split(string(b), "\n\n") {
		if !strings.Contains(blk, `N: Name="`+name+`"`) {
			continue
		}
		for _, l := range strings.Split(blk, "\n") {
			if h, ok := strings.CutPrefix(l, "H: Handlers="); ok {
				for _, f := range strings.Fields(h) {
					if strings.HasPrefix(f, "event") {
						path = root + "/dev/input/" + f
					}
				}
			}
			if sw, ok := strings.CutPrefix(l, "B: SW="); ok && strings.Trim(sw, "0 ") != "" {
				hasSwitch = true
			}
		}
		if path != "" {
			return path, hasSwitch, nil
		}
	}
	return "", false, fmt.Errorf("input device %q not found", name)
}

// SwitchState returns the current value of switch `code` (EVIOCGSW), e.g. the mic-mute latch.
func SwitchState(f *os.File, code int) (bool, error) {
	var bits [8]byte // SW_CNT is 0x10 bits = 2 bytes; 8 is safe headroom
	// _IOC(_IOC_READ=2, 'E', 0x1b, len) — built as uint32 so the dir bit (<<30) doesn't
	// overflow a 32-bit int on armv7.
	const iocRead uint32 = 2
	req := uintptr(iocRead<<30 | uint32(len(bits))<<16 | 'E'<<8 | 0x1b)
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), req, uintptr(unsafe.Pointer(&bits[0])))
	if errno != 0 {
		return false, errno
	}
	return bits[code/8]&(1<<(code%8)) != 0, nil
}

// AbsInfo is the kernel's input_absinfo for one EV_ABS axis.
type AbsInfo struct {
	Value      int32
	Minimum    int32
	Maximum    int32
	Fuzz       int32
	Flat       int32
	Resolution int32
}

// AbsoluteInfo returns EVIOCGABS(code). Querying the axis at runtime avoids baking a controller's
// coordinate range into the board profile.
func AbsoluteInfo(f *os.File, code uint16) (AbsInfo, error) {
	var info AbsInfo
	const (
		iocRead      uint32 = 2
		absInfoBytes        = uint32(unsafe.Sizeof(info))
	)
	req := uintptr(iocRead<<30 | absInfoBytes<<16 | 'E'<<8 | uint32(0x40+code))
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), req, uintptr(unsafe.Pointer(&info)))
	if errno != 0 {
		return AbsInfo{}, errno
	}
	if info.Maximum <= info.Minimum {
		return AbsInfo{}, fmt.Errorf("ABS %d invalid range %d..%d", code, info.Minimum, info.Maximum)
	}
	return info, nil
}

// CircularAxis converts a wrapped EV_ABS angle into evenly spaced directional steps.
type CircularAxis struct {
	min, max     int32
	last         int32
	accum        int64
	stepsPerTurn int64
	gap          time.Duration
	lastAt       time.Time
	haveLast     bool
}

// NewCircularAxis splits a full turn of the axis into stepsPerTurn steps; a pause longer than
// gestureGap starts a new gesture.
func NewCircularAxis(info AbsInfo, stepsPerTurn int, gestureGap time.Duration) *CircularAxis {
	return &CircularAxis{
		min:          info.Minimum,
		max:          info.Maximum,
		stepsPerTurn: int64(stepsPerTurn),
		gap:          gestureGap,
	}
}

// Move returns signed steps since the previous sample. The first sample of a gesture establishes
// its baseline, and crossing the maximum/minimum boundary preserves direction.
func (c *CircularAxis) Move(value int32, now time.Time) int {
	if value < c.min || value > c.max || c.stepsPerTurn <= 0 {
		return 0
	}
	if !c.haveLast || now.Sub(c.lastAt) > c.gap {
		c.last, c.lastAt, c.haveLast, c.accum = value, now, true, 0
		return 0
	}
	span := int64(c.max) - int64(c.min) + 1
	delta := int64(value) - int64(c.last)
	if delta > span/2 {
		delta -= span
	} else if delta < -span/2 {
		delta += span
	}
	c.last, c.lastAt = value, now
	c.accum += delta
	threshold := max(int64(1), span/c.stepsPerTurn)
	steps := c.accum / threshold
	c.accum -= steps * threshold
	return int(steps)
}
