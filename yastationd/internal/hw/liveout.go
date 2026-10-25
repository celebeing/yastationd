package hw

import (
	"context"
	"os"
	"strconv"
	"syscall"
	"time"
)

// LiveOut plays a live stream (the intercom's far end) with as little buffering as aplay allows:
// its own dmix client beside Player (whose Play/Stream/Stop interrupt each other, so a TTS reply
// or a «стоп» never cuts it), a 60 ms ALSA buffer, and a one-page pipe. The writer's blocking
// Write is then paced by the DAC, which is what the intercom's jitter buffer measures drift by.
// The default 64 KiB pipe alone would hold ~0.7 s at 48 kHz mono.
type LiveOut struct {
	w      *os.File
	cancel context.CancelFunc
	done   chan struct{}
}

const fSetPipeSz = 1031 // F_SETPIPE_SZ (F_LINUX_SPECIFIC_BASE + 7)

// StartLiveOut starts aplay on raw S16LE mono at rate.
func StartLiveOut(device string, rate int) (*LiveOut, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	// Best effort: the kernel rounds up to a page; a failure only costs latency.
	syscall.Syscall(syscall.SYS_FCNTL, w.Fd(), fSetPipeSz, 4096)
	ctx, cancel := context.WithCancel(context.Background())
	cmd := aplayCmd(ctx, "-q", "-D", device, "-t", "raw", "-f", "S16_LE", "-r", strconv.Itoa(rate),
		"-c", "1", "--buffer-time=60000", "--period-time=20000")
	cmd.Stdin = r
	if err := cmd.Start(); err != nil {
		cancel()
		r.Close()
		w.Close()
		return nil, err
	}
	r.Close() // aplay holds its end
	o := &LiveOut{w: w, cancel: cancel, done: make(chan struct{})}
	go func() {
		cmd.Wait()
		close(o.done)
	}()
	return o, nil
}

// Write blocks until aplay has room (at the DAC's pace).
func (o *LiveOut) Write(p []byte) (int, error) { return o.w.Write(p) }

// Close ends the stream: EOF lets aplay drain its few tens of ms and exit; SIGTERM (via the
// context, never a bare SIGKILL — see aplayCmd) if it doesn't.
func (o *LiveOut) Close() {
	o.w.Close()
	select {
	case <-o.done:
	case <-time.After(500 * time.Millisecond):
		o.cancel()
		<-o.done
	}
	o.cancel()
}
