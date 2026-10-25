// Command intercom-replay runs a recorded raw uplink (intercom_record_dir: S16LE, 16 kHz mono,
// the VQE stream before the guard) through the same uplink processing as a call, and writes the
// result as a WAV — to tune the level and the gate on real audio without making calls.
//
//	intercom-replay [-gain 30] in.raw out.wav
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/intercom"
)

func main() {
	gain := flag.Float64("gain", 0, "uplink gain in dB (0 = the daemon's default)")
	flag.Parse()
	if flag.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "usage: intercom-replay [-gain dB] in.raw out.wav")
		os.Exit(2)
	}
	in, err := os.ReadFile(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	g := &intercom.Guard{UpGainDB: *gain}
	out := make([]byte, 44, 44+len(in))
	now := time.Now()
	var fr intercom.Framer
	fr.Push(in, func(f []int16) {
		g.Up(f, now)
		now = now.Add(intercom.FrameDur)
		for _, v := range f {
			out = binary.LittleEndian.AppendUint16(out, uint16(v))
		}
	})
	n := len(out) - 44
	copy(out, "RIFF")
	binary.LittleEndian.PutUint32(out[4:], uint32(36+n))
	copy(out[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(out[16:], 16)
	binary.LittleEndian.PutUint16(out[20:], 1)
	binary.LittleEndian.PutUint16(out[22:], 1)
	binary.LittleEndian.PutUint32(out[24:], intercom.Rate)
	binary.LittleEndian.PutUint32(out[28:], intercom.Rate*2)
	binary.LittleEndian.PutUint16(out[32:], 2)
	binary.LittleEndian.PutUint16(out[34:], 16)
	copy(out[36:], "data")
	binary.LittleEndian.PutUint32(out[40:], uint32(n))
	if err := os.WriteFile(flag.Arg(1), out, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	st := g.Uplink()
	fmt.Printf("floor %.1f dBFS, gate open %d of %d frames\n", st.FloorDB, st.OpenFrames, st.Frames)
}
