// yastation-companion: the computer-side helper of the yastationd speakers (companion/README.md).
//
//	yastation-companion record [-d 60s] [-o file]   what the computer plays → file.raw + file.json
//
// record writes S16LE mono at the device's rate (silence where the device delivered nothing) and
// a JSON sidecar with the rate and the wall-clock time of the first sample: the reference side of
// the pulse experiments (docs/pulse.md).
package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"os/signal"
	"time"

	"github.com/celebeing/yastationd/companion/internal/capture"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "record":
		if err := record(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "record:", err)
			os.Exit(1)
		}
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: yastation-companion record [-d 60s] [-o file]")
	os.Exit(2)
}

type recordMeta struct {
	Rate        int    `json:"rate_hz"`
	Channels    int    `json:"device_channels"`
	Device      string `json:"device"`
	Format      string `json:"format"`
	StartUnixNS int64  `json:"start_unix_ns"` // wall clock of sample 0
	Samples     uint64 `json:"samples"`
}

func record(args []string) error {
	fs := flag.NewFlagSet("record", flag.ExitOnError)
	dur := fs.Duration("d", 60*time.Second, "how long")
	out := fs.String("o", "loopback-"+time.Now().Format("20060102-150405"), "output name (.raw and .json are added)")
	fs.Parse(args)

	f, err := os.Create(*out + ".raw")
	if err != nil {
		return err
	}
	defer f.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *dur)
	defer cancel()

	var meta recordMeta
	var written uint64
	var sq float64
	var nsq int
	lastPrint := time.Now()
	buf := make([]byte, 0, 8192)
	err = capture.Loopback(ctx, func(s capture.Stream) {
		meta = recordMeta{Rate: s.Rate, Channels: s.Channels, Device: s.Device, Format: "s16le mono",
			StartUnixNS: s.Start.UnixNano()}
		fmt.Fprintf(os.Stderr, "capturing %s: %d Hz, %d ch\n", s.Device, s.Rate, s.Channels)
	}, func(pos uint64, mono []float32) {
		buf = buf[:0]
		if pos > written+uint64(meta.Rate)*600 { // not a pause: a position that makes no sense
			fmt.Fprintf(os.Stderr, "  position jumped by %d samples: not padded\n", pos-written)
			written = pos
		}
		for ; written < pos; written++ { // a gap: the device delivered nothing
			buf = binary.LittleEndian.AppendUint16(buf, 0)
			if len(buf) >= 8192 {
				f.Write(buf)
				buf = buf[:0]
			}
		}
		for _, v := range mono {
			x := math.Max(-1, math.Min(1, float64(v)))
			buf = binary.LittleEndian.AppendUint16(buf, uint16(int16(x*32767)))
			sq += x * x
			nsq++
		}
		f.Write(buf)
		written = pos + uint64(len(mono))
		if time.Since(lastPrint) >= 5*time.Second && nsq > 0 {
			fmt.Fprintf(os.Stderr, "  %6.1f s  level %.1f dBFS\n", float64(written)/float64(meta.Rate),
				10*math.Log10(sq/float64(nsq)+1e-12))
			sq, nsq, lastPrint = 0, 0, time.Now()
		}
	})
	if err != nil {
		return err
	}
	meta.Samples = written
	j, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(*out+".json", append(j, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %s.raw (%d samples) and %s.json\n", *out, written, *out)
	return nil
}
