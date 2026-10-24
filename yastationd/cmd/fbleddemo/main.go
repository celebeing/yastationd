// fbleddemo exercises the front framebuffer LED display. Dev tool: stop yastationd while it
// runs, because both processes would write the same framebuffer.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/display"
	"github.com/celebeing/yastationd/yastationd/internal/hw"
)

func main() {
	uwu := flag.Bool("uwu", false, "show UwU on the front dot-matrix screen")
	clock := flag.Bool("clock", false, "show the daemon's pixel clock renderer")
	flag.Parse()

	if *uwu == *clock {
		fmt.Fprintln(os.Stderr, "select exactly one mode: -uwu or -clock")
		os.Exit(2)
	}

	dev := hw.FindDotMatrix("")
	if dev == "" {
		fmt.Fprintln(os.Stderr, "no dot-matrix screen found (looked for /sys/class/graphics/fb*/name *LED screen*)")
		os.Exit(1)
	}
	scr, err := hw.OpenDotMatrix(dev)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer scr.Close()

	if *uwu {
		runUwu(scr, dev)
		return
	}
	runClock(scr, dev)
}

func runUwu(scr *hw.DotMatrix, dev string) {
	w, h := scr.Size()
	fmt.Printf("uwu on %s, %dx%d — Ctrl-C to blank and exit\n", dev, w, h)
	fmt.Println("(the gowin glass lags one fb write behind; the loop below re-sends the")
	fmt.Println(" frame every 200 ms so it stays on — watch for the flash at every latch)")
	px := display.UwuFrame(w, h, hw.RGB{R: 255, G: 80, B: 160})
	if err := scr.Show(px); err != nil {
		fatal(err)
	}
	stop := make(chan struct{})
	refreshDone := scr.PeriodicRefresh(200*time.Millisecond, stop)
	waitForSignal()
	close(stop)
	<-refreshDone // no old UwU write may race with the blank frame below
	blankAndFlush(scr, w, h)
}

func runClock(scr *hw.DotMatrix, dev string) {
	w, h := scr.Size()
	fmt.Printf("clock on %s, %dx%d — Ctrl-C to blank and exit\n", dev, w, h)
	sig := signalChannel()
	tick := time.NewTicker(66 * time.Millisecond) // exact daemon render cadence (~15 Hz)
	defer tick.Stop()
	for {
		frame := display.Clock(time.Now(), time.Local, 180)
		if err := scr.Show(display.Seg7ToPixels(frame, w, h, hw.RGB{R: 255, G: 255, B: 255})); err != nil {
			fatal(err)
		}
		select {
		case <-tick.C:
		case <-sig:
			blankAndFlush(scr, w, h)
			return
		}
	}
}

func blankAndFlush(scr *hw.DotMatrix, w, h int) {
	if err := scr.Show(make([]hw.RGB, w*h)); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	if err := scr.Flush(); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}

func waitForSignal() {
	<-signalChannel()
}

func signalChannel() <-chan os.Signal {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	return sig
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
