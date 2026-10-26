package hw

import (
	"os"
	"strconv"
)

// Backlight is a screen's sysfs backlight brightness (e.g. orion's
// /sys/class/backlight/fb1/brightness, 0..255). Set writes only on a change.
type Backlight struct {
	path string
	last int
}

// NewBacklight returns the backlight at path, or nil when there is none.
func NewBacklight(path string) *Backlight {
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	return &Backlight{path: path, last: -1}
}

// Set writes v (clamped to 0..255) unless it is the value already written. Nil-safe.
func (b *Backlight) Set(v int) error {
	if b == nil {
		return nil
	}
	v = min(max(v, 0), 255)
	if v == b.last {
		return nil
	}
	if err := os.WriteFile(b.path, []byte(strconv.Itoa(v)), 0o644); err != nil {
		return err
	}
	b.last = v
	return nil
}
