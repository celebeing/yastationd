// ringdemo cycles the daemon's ring effects on the real LEDs. Dev tool: stop yastationd while
// it runs, because both processes would write the same LEDs.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/board"
	"github.com/celebeing/yastationd/yastationd/internal/config"
	"github.com/celebeing/yastationd/yastationd/internal/display"
	"github.com/celebeing/yastationd/yastationd/internal/hw"
)

type demo struct {
	name   string
	render func() display.RenderFunc[display.RingFrame]
}

func main() {
	per := flag.Duration("per", 6*time.Second, "time per effect")
	boardName := flag.String("board", "auto", "board profile (auto = detect)")
	flag.Parse()

	prof, err := board.Load("", *boardName, config.CFWRoot()+"/board.conf")
	if err != nil || prof.Ring.Kind != "sysfs-rgb" {
		fmt.Fprintln(os.Stderr, "no ring on this board:", err)
		os.Exit(1)
	}
	ring, err := hw.NewRing("", prof.Ring.Pixels, prof.Ring.LED, prof.Ring.Colors)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var matrix *hw.Matrix
	if m := prof.Matrix; m.Kind == "seg7" {
		matrix = hw.NewMatrix("", m.Segment, m.Colon, m.Dot, m.Degree)
	}
	blank := display.Blank(ring.Pixels())
	solid := func(c hw.RGB) func() display.RenderFunc[display.RingFrame] {
		return func() display.RenderFunc[display.RingFrame] {
			return func(_ time.Time, below display.RingFrame) (display.RingFrame, bool) {
				return display.Solid(len(below), c), false
			}
		}
	}
	demos := []demo{
		{"listening: pulse cyan 1.5s", func() display.RenderFunc[display.RingFrame] {
			return display.Pulse(display.Cyan, 1500*time.Millisecond)
		}},
		{"thinking: comet white 0.9s", func() display.RenderFunc[display.RingFrame] {
			return display.Comet(display.White, 900*time.Millisecond)
		}},
		{"boot: comet cyan 1.2s", func() display.RenderFunc[display.RingFrame] {
			return display.Comet(display.Cyan, 1200*time.Millisecond)
		}},
		{"slow comet green 3s", func() display.RenderFunc[display.RingFrame] { return display.Comet(display.Green, 3*time.Second) }},
		{"speaking: pulse green 0.7s", func() display.RenderFunc[display.RingFrame] {
			return display.Pulse(display.Green, 700*time.Millisecond)
		}},
		{"error: blink red 0.3s", func() display.RenderFunc[display.RingFrame] { return display.Blink(display.Red, 300*time.Millisecond) }},
		{"mute: solid amber", solid(display.Amber)},
		{"booting: blue pulse 2s", func() display.RenderFunc[display.RingFrame] { return display.Pulse(display.Blue, 2*time.Second) }},
		{"first connection failed: red pulse 2s", func() display.RenderFunc[display.RingFrame] { return display.Pulse(display.Red, 2*time.Second) }},
		{"degraded: dim red pulse 3s", func() display.RenderFunc[display.RingFrame] {
			return display.Pulse(display.Red.Scale(40), 3*time.Second)
		}},
		{"rainbow: hue wheel rotating 3s", func() display.RenderFunc[display.RingFrame] { return display.Rainbow(3*time.Second, 255) }},
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	tick := time.NewTicker(66 * time.Millisecond)
	defer tick.Stop()
	for {
		for i, d := range demos {
			fmt.Printf("%d %s\n", i+1, d.name)
			matrix.Show(display.Number(i+1, 128))
			r := d.render()
			end := time.Now().Add(*per)
			for now := time.Now(); now.Before(end); {
				select {
				case <-sig:
					ring.Show(blank)
					matrix.Show(display.MatrixFrame{})
					return
				case now = <-tick.C:
					f, _ := r(now, blank)
					ring.Show(f)
				}
			}
			ring.Show(blank)
			time.Sleep(400 * time.Millisecond)
		}
	}
}
