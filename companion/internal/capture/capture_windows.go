//go:build windows

package capture

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
)

// WASAPI loopback: the default render endpoint opened in shared mode with the loopback flag gives
// what it plays, in its mix format (as a rule 32-bit float, 48 kHz, stereo). Polled every 10 ms.
//
// Formats: WAVE_FORMAT_IEEE_FLOAT (3), WAVE_FORMAT_PCM (1) at 16 bits, and WAVE_FORMAT_EXTENSIBLE
// (0xFFFE), whose sub-format is read from the bytes after WAVEFORMATEX.
const (
	fmtPCM        = 1
	fmtFloat      = 3
	fmtExtensible = 0xFFFE
)

func loopback(ctx context.Context, started func(Stream), h Handler) error {
	runtime.LockOSThread() // COM objects live on the thread that made them
	defer runtime.UnlockOSThread()
	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
		return fmt.Errorf("capture: CoInitializeEx: %w", err)
	}
	defer ole.CoUninitialize()

	var mmde *wca.IMMDeviceEnumerator
	if err := wca.CoCreateInstance(wca.CLSID_MMDeviceEnumerator, 0, wca.CLSCTX_ALL, wca.IID_IMMDeviceEnumerator, &mmde); err != nil {
		return fmt.Errorf("capture: device enumerator: %w", err)
	}
	defer mmde.Release()
	var mmd *wca.IMMDevice
	if err := mmde.GetDefaultAudioEndpoint(wca.ERender, wca.EConsole, &mmd); err != nil {
		return fmt.Errorf("capture: default output device: %w", err)
	}
	defer mmd.Release()
	var ac *wca.IAudioClient
	if err := mmd.Activate(wca.IID_IAudioClient, wca.CLSCTX_ALL, nil, &ac); err != nil {
		return fmt.Errorf("capture: audio client: %w", err)
	}
	defer ac.Release()
	var wfx *wca.WAVEFORMATEX
	if err := ac.GetMixFormat(&wfx); err != nil {
		return fmt.Errorf("capture: mix format: %w", err)
	}
	defer ole.CoTaskMemFree(uintptr(unsafe.Pointer(wfx)))

	tag := wfx.WFormatTag
	if tag == fmtExtensible && wfx.CbSize >= 22 {
		// WAVEFORMATEXTENSIBLE: after the 18 bytes of WAVEFORMATEX, 2 (valid bits) + 4 (channel
		// mask) bytes, then the sub-format GUID whose first two bytes are the format tag.
		ext := (*[40]byte)(unsafe.Pointer(wfx))
		tag = uint16(ext[24]) | uint16(ext[25])<<8
	}
	bits := int(wfx.WBitsPerSample)
	ch := int(wfx.NChannels)
	switch {
	case tag == fmtFloat && bits == 32:
	case tag == fmtPCM && bits == 16:
	default:
		return fmt.Errorf("capture: mix format tag %#x, %d bits: not handled", tag, bits)
	}

	const buffer = 10_000_000 // 1 s, in 100 ns units
	if err := ac.Initialize(wca.AUDCLNT_SHAREMODE_SHARED, wca.AUDCLNT_STREAMFLAGS_LOOPBACK, buffer, 0, wfx, nil); err != nil {
		return fmt.Errorf("capture: initialize loopback: %w", err)
	}
	var acc *wca.IAudioCaptureClient
	if err := ac.GetService(wca.IID_IAudioCaptureClient, &acc); err != nil {
		return fmt.Errorf("capture: capture client: %w", err)
	}
	defer acc.Release()
	if err := ac.Start(); err != nil {
		return fmt.Errorf("capture: start: %w", err)
	}
	defer ac.Stop()

	rate := int(wfx.NSamplesPerSec)
	first := true
	var base uint64
	var mono []float32
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
		for {
			var n uint32
			if err := acc.GetNextPacketSize(&n); err != nil {
				return fmt.Errorf("capture: packet size: %w", err)
			}
			if n == 0 {
				break
			}
			var data *byte
			var frames, flags uint32
			var devPos, qpc uint64
			if err := acc.GetBuffer(&data, &frames, &flags, &devPos, &qpc); err != nil {
				return fmt.Errorf("capture: get buffer: %w", err)
			}
			if first {
				// The device position counts from when the device started, not from our
				// stream: the first packet's is position 0. The wall clock is read now, a few
				// ms after the packet was captured at most.
				first = false
				base = devPos
				started(Stream{Rate: rate, Channels: ch, Device: "default output (WASAPI loopback)", Start: time.Now()})
			}
			if cap(mono) < int(frames) {
				mono = make([]float32, frames)
			}
			mono = mono[:frames]
			if flags&wca.AUDCLNT_BUFFERFLAGS_SILENT != 0 || data == nil {
				clear(mono)
			} else {
				toMono(mono, unsafe.Pointer(data), ch, tag)
			}
			if err := acc.ReleaseBuffer(frames); err != nil {
				return fmt.Errorf("capture: release buffer: %w", err)
			}
			if devPos >= base {
				h(devPos-base, mono)
			}
		}
	}
}

// toMono averages the channels of n interleaved frames at p into out.
func toMono(out []float32, p unsafe.Pointer, ch int, tag uint16) {
	n := len(out)
	if tag == fmtFloat {
		s := unsafe.Slice((*float32)(p), n*ch)
		for i := range out {
			var sum float32
			for c := 0; c < ch; c++ {
				sum += s[i*ch+c]
			}
			out[i] = sum / float32(ch)
		}
		return
	}
	s := unsafe.Slice((*int16)(p), n*ch)
	for i := range out {
		var sum float32
		for c := 0; c < ch; c++ {
			sum += float32(s[i*ch+c]) / math.MaxInt16
		}
		out[i] = sum / float32(ch)
	}
}
