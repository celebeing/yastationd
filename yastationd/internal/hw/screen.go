package hw

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// fbdev geometry ioctls (linux/fb.h). We only ever *read* the screen info: the console is
// not reconfigured, no FBIOPUT_* is issued.
const (
	fbGetVarScreeninfo = 0x4600
	fbGetFixScreeninfo = 0x4602
)

// fb_bitfield: where a channel sits inside a pixel (offset in bits).
type fbBitfield struct {
	offset, length, msbRight uint32
}

// fb_var_screeninfo: all-u32 layout, identical on 32/64-bit — no ABI pitfalls.
type fbVarScreeninfo struct {
	xres, yres, xresVirtual, yresVirtual         uint32
	xoffset, yoffset, bitsPerPixel, gray         uint32
	red, green, blue, transp                     fbBitfield
	nonstd, activate, height, width, accelFlg    uint32
	pixclock, lMargin, rMargin, uMargin, bMargin uint32
	hsyncLen, vsyncLen, sync, vmode, rotate      uint32
	colorspace                                   uint32
	reserved                                     [4]uint32
}

// fb_fix_screeninfo: two unsigned long fields (uintptr mirrors them per-arch, as on armv7
// and aarch64 alike the C long matches Go's uintptr size).
type fbFixScreeninfo struct {
	id           [16]byte
	smemStart    uintptr // unsigned long
	smemLen      uint32
	typ          uint32
	typeAux      uint32
	visual       uint32
	xpanstep     uint16
	ypanstep     uint16
	ywrapstep    uint16
	lineLength   uint32
	mmioStart    uintptr // unsigned long
	mmioLen      uint32
	accel        uint32
	capabilities uint16
	reserved     [2]uint16
}

// DotMatrix is a front dot-matrix screen behind a plain framebuffer — orion's 21x12
// "YNDX LED screen" (gowin FPGA) on /dev/fb1. Geometry, stride and channel order come
// from the fbdev ioctls, never guessed. A nil *DotMatrix shows nothing (a no-op Show),
// so callers can treat "no screen" like the nil *Matrix of the 7-seg path.
type DotMatrix struct {
	f       *os.File
	w, h    int
	stride  int    // fix.line_length: bytes per row, may exceed w*pixelBytes
	pb      int    // bytes per pixel
	order   [3]int // byte offset of R, G, B inside a pixel
	scratch []byte
}

// OpenDotMatrix opens dev (e.g. /dev/fb1) and reads its layout. The pixel format must be
// 8-bit-per-channel (24/32 bpp); packed 16-bit colour needs a real backend decision.
func OpenDotMatrix(dev string) (*DotMatrix, error) {
	f, err := os.OpenFile(dev, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	var v fbVarScreeninfo
	if err := fbIoctl(f.Fd(), fbGetVarScreeninfo, unsafe.Pointer(&v)); err != nil {
		f.Close()
		return nil, fmt.Errorf("dotmatrix %s: vscreeninfo: %w", dev, err)
	}
	var fx fbFixScreeninfo
	if err := fbIoctl(f.Fd(), fbGetFixScreeninfo, unsafe.Pointer(&fx)); err != nil {
		f.Close()
		return nil, fmt.Errorf("dotmatrix %s: fscreeninfo: %w", dev, err)
	}
	m := &DotMatrix{f: f, w: int(v.xres), h: int(v.yres), pb: int(v.bitsPerPixel) / 8}
	if m.w <= 0 || m.h <= 0 || v.bitsPerPixel%8 != 0 || m.pb < 3 {
		f.Close()
		return nil, fmt.Errorf("dotmatrix %s: %dx%d @%d bpp not supported", dev, m.w, m.h, v.bitsPerPixel)
	}
	for i, ch := range []fbBitfield{v.red, v.green, v.blue} {
		if ch.length != 8 || ch.offset%8 != 0 || int(ch.offset/8) >= m.pb {
			f.Close()
			return nil, fmt.Errorf("dotmatrix %s: channel not a byte (len=%d off=%d)", dev, ch.length, ch.offset)
		}
		m.order[i] = int(ch.offset / 8)
	}
	m.stride = int(fx.lineLength)
	if m.stride < m.w*m.pb {
		m.stride = m.w * m.pb // no/pad-less line_length: assume tight rows
	}
	m.scratch = make([]byte, m.stride*m.h)
	return m, nil
}

// Size returns the matrix dimensions (0, 0 for a nil DotMatrix).
func (m *DotMatrix) Size() (int, int) {
	if m == nil {
		return 0, 0
	}
	return m.w, m.h
}

// Show writes a whole frame: px holds w*h colours row-major, top row first. Padding bytes
// between rows stay black (zero). On a nil DotMatrix it is a no-op; the frame is one
// WriteAt, so no other writer sees torn rows.
func (m *DotMatrix) Show(px []RGB) error {
	if m == nil {
		return nil
	}
	if len(px) != m.w*m.h {
		return fmt.Errorf("dotmatrix: %d px, want %d", len(px), m.w*m.h)
	}
	_, err := m.f.WriteAt(m.encode(px), 0)
	return err
}

// Close releases the framebuffer descriptor. It is nil-safe.
func (m *DotMatrix) Close() error {
	if m == nil || m.f == nil {
		return nil
	}
	err := m.f.Close()
	m.f = nil
	return err
}

// encode lays px into m.scratch (kept zeroed outside the pixel area; never rewritten
// beyond row end, so stride padding stays black).
func (m *DotMatrix) encode(px []RGB) []byte {
	for y := 0; y < m.h; y++ {
		row := m.scratch[y*m.stride : (y+1)*m.stride]
		src := px[y*m.w : (y+1)*m.w]
		for x, c := range src {
			p := row[x*m.pb : (x+1)*m.pb]
			p[m.order[0]], p[m.order[1]], p[m.order[2]] = c.R, c.G, c.B
		}
	}
	return m.scratch
}

func fbIoctl(fd, req uintptr, arg unsafe.Pointer) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(arg)); errno != 0 {
		return errno
	}
	return nil
}

// FindDotMatrix scans /sys/class/graphics/fb*/name for the vendor dot-matrix screen
// (name contains "LED screen" — orion's says "YNDX LED screen") and returns its /dev/fbN
// under root ("" if absent). Root is a sysfs prefix for tests, "" on the device.
func FindDotMatrix(root string) string {
	for i := 0; i < 16; i++ {
		p := root + "/sys/class/graphics/fb" + strconv.Itoa(i) + "/name"
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if strings.Contains(strings.TrimSpace(string(b)), "LED screen") {
			return root + "/dev/fb" + strconv.Itoa(i)
		}
	}
	return ""
}

// Flush re-writes the last frame once — for the one-behind glass this is what latches
// the pending frame so the previous Show actually reaches the glass (see PeriodicRefresh).
func (m *DotMatrix) Flush() error {
	if m == nil {
		return nil
	}
	_, err := m.f.WriteAt(m.scratch, 0)
	return err
}

// PeriodicRefresh keeps flushing the last frame to the device until stop closes, in its
// own goroutine, and returns a channel closed after the goroutine exits. The caller MUST
// wait for done before changing the frame: scratch is shared to avoid one allocation per
// refresh. The gowin glass lags ONE write behind — a lone Show paints nothing; the next
// write makes it appear (on-device 2026-10-24).
func (m *DotMatrix) PeriodicRefresh(interval time.Duration, stop <-chan struct{}) <-chan struct{} {
	done := make(chan struct{})
	if m == nil {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				m.f.WriteAt(m.scratch, 0) // errors surface via the next Show
			}
		}
	}()
	return done
}
