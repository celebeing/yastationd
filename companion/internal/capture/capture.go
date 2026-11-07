// Package capture takes what the computer plays (its "loopback": the mix the default output
// device gets) as mono float samples. One backend per OS behind Loopback; Windows has WASAPI's
// loopback mode, the others are not written yet (docs/architecture.md, "Platforms").
package capture

import (
	"context"
	"errors"
	"time"
)

// Stream describes a running capture.
type Stream struct {
	Rate     int       // samples per second
	Channels int       // of the device's mix (the blocks are mono, the channels averaged)
	Device   string    // what the backend opened, for the logs
	Start    time.Time // the wall-clock time of stream position 0
}

// Handler gets the blocks in order. pos is the stream position (in samples) of mono[0]: a jump
// past the end of the previous block is time the device delivered nothing (Windows sends no
// packets while nothing plays), to be read as silence.
type Handler func(pos uint64, mono []float32)

// ErrUnsupported: no loopback backend for this OS yet.
var ErrUnsupported = errors.New("capture: no loopback capture on this OS yet")

// Loopback captures the default output device's mix until ctx ends. started is called once,
// before the first block.
func Loopback(ctx context.Context, started func(Stream), h Handler) error {
	return loopback(ctx, started, h)
}
