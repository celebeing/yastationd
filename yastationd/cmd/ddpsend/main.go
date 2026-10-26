// ddpsend streams test effects to a DDP receiver (a speaker's ambilight, docs/ambilight-stream.md)
// from a computer, standing in for LedFx while there is none:
//
//	go run ./cmd/ddpsend -to <speaker-ip> [-n 12] [-fx rainbow|chase|pulse|bars] [-fps 50] [-for 20s]
package main

import (
	"flag"
	"fmt"
	"math"
	"net"
	"os"
	"time"
)

func main() {
	to := flag.String("to", "", "receiver host[:port] (port 4048 by default)")
	n := flag.Int("n", 12, "pixels")
	fx := flag.String("fx", "rainbow", "rainbow | chase | pulse | bars")
	fps := flag.Int("fps", 50, "frames per second")
	dur := flag.Duration("for", 20*time.Second, "how long")
	flag.Parse()
	if *to == "" {
		flag.Usage()
		os.Exit(2)
	}
	addr := *to
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, "4048")
	}
	c, err := net.Dial("udp", addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	start := time.Now()
	tick := time.NewTicker(time.Second / time.Duration(*fps))
	defer tick.Stop()
	var seq byte
	for now := range tick.C {
		t := now.Sub(start).Seconds()
		if now.Sub(start) > *dur {
			break
		}
		px := make([]byte, 3**n)
		for i := 0; i < *n; i++ {
			r, g, b := frame(*fx, i, *n, t)
			px[3*i], px[3*i+1], px[3*i+2] = r, g, b
		}
		seq = seq%15 + 1
		hdr := []byte{0x41, seq, 0x0b, 1, 0, 0, 0, 0, byte(len(px) >> 8), byte(len(px))}
		if _, err := c.Write(append(hdr, px...)); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	}
	// A black frame at the end; the speaker's own state is back once frames stop.
	c.Write(append([]byte{0x41, 0, 0x0b, 1, 0, 0, 0, 0, byte(3 * *n >> 8), byte(3 * *n)}, make([]byte, 3**n)...))
}

func frame(fx string, i, n int, t float64) (r, g, b byte) {
	switch fx {
	case "chase": // one dot running top to bottom and back
		pos := int(math.Abs(math.Mod(t*8, float64(2*n-2))-float64(n-1)) + 0.5)
		if i == pos {
			return 0, 160, 255
		}
		return 0, 0, 0
	case "pulse": // the whole strip breathing at 120 bpm, warm
		k := math.Pow(0.5+0.5*math.Cos(t*2*math.Pi*2), 3)
		return byte(255 * k), byte(80 * k), byte(10 * k)
	case "bars": // a level rising from the bottom (the last pixel) like a VU meter
		level := (0.5 + 0.5*math.Sin(t*3)) * float64(n)
		if float64(n-1-i) < level {
			h := float64(n-1-i) / float64(n)
			return hsv(0.33 - 0.33*h)
		}
		return 0, 0, 0
	}
	return hsv(math.Mod(t*0.2+float64(i)/float64(n), 1)) // rainbow
}

func hsv(h float64) (r, g, b byte) {
	f := func(n float64) byte {
		k := math.Mod(n+h*6, 6)
		return byte(255 * (1 - math.Max(0, math.Min(math.Min(k, 4-k), 1))))
	}
	return f(5), f(3), f(1)
}
