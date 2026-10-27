// Command ledsim runs yastationd on the host with a board's LED surfaces simulated, for the
// browser preview (docs/led-preview.md). It is the daemon itself — the same scenes, animations,
// overlays and timing as on a unit — over a throwaway sysroot: sysfs LED files for the ring and
// the ambilight, a virtual front screen, and the firmware's screen patterns from a local copy.
// No MQTT, no wake word, and no audio in practice (the host's aplay does not know the board's
// playback device; the video mode then plays silently); commands come through the frame tap.
//
//	go run ./cmd/ledsim -patterns <dir with the firmware's ledpatterns/screen> [-media <dir>]
//	open http://localhost:8930/                       (add ?src=http://<unit>:8930 to compare)
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/celebeing/yastationd/yastationd/internal/app"
	"github.com/celebeing/yastationd/yastationd/internal/board"
	"github.com/celebeing/yastationd/yastationd/internal/config"
)

func main() {
	boardName := flag.String("board", "orion", "board profile")
	screen := flag.String("screen", "21x12", "front screen size for a dot-matrix board (WxH)")
	patterns := flag.String("patterns", "", "the firmware's screen patterns (ledpatterns/screen of a dump or a unit)")
	media := flag.String("media", "", "directory with the video mode's files (video.led[.gz], video.wav from cmd/videoconv)")
	addr := flag.String("addr", "localhost:8930", "frame tap listen address")
	web := flag.String("http", "", "also serve the web app here (e.g. localhost:8940; login admin / yastation)")
	dir := flag.String("dir", "", "sysroot to (re)use; default: a fresh temporary one")
	verbose := flag.Bool("v", false, "debug log")
	flag.Parse()

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	if err := run(log, *boardName, *screen, *patterns, *media, *addr, *web, *dir); err != nil {
		log.Error("ledsim", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger, name, screen, patterns, media, addr, web, root string) error {
	prof, err := board.Builtin(name)
	if err != nil {
		return err
	}
	if root == "" {
		if root, err = os.MkdirTemp("", "ledsim-"); err != nil {
			return err
		}
		defer os.RemoveAll(root)
	}
	if err := sysroot(root, prof, screen, patterns, media); err != nil {
		return err
	}

	cfg := config.Default()
	cfg.SysRoot = root
	cfg.Board = name
	cfg.BoardFile = "/board.conf"
	cfg.DeviceID, cfg.DeviceName = "ledsim", "LED simulator ("+name+")"
	cfg.MQTTHost = ""
	cfg.WakeEnabled, cfg.Sendspin, cfg.WifiSetup, cfg.Intercom, cfg.TimeSync = false, false, false, false, false
	cfg.WyomingPort = 0
	cfg.Probes = nil
	cfg.AutoBrightness = false
	cfg.RoomSound = false
	cfg.NetWatchdog = 0 // never reboot the host
	cfg.HTTPAddr = web  // off by default: the frame tap serves the preview
	cfg.FrameTapAddr = addr
	cfg.LogLevel = "info"

	a, err := app.New(cfg, log)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	log.Info("ledsim: open the preview", "url", "http://"+strings.Replace(addr, "0.0.0.0", "localhost", 1)+"/")
	return a.Run(ctx)
}

// sysroot lays out what the daemon opens: an LED class directory per ring/ambilight LED, the
// board.conf override that makes the front screen virtual, the CFW directories, and the
// firmware's pattern directory linked to the local copy.
func sysroot(root string, p board.Profile, screen, patterns, media string) error {
	var leds []string
	for i := 0; i < p.Ring.Pixels; i++ {
		for _, c := range p.Ring.Colors {
			leds = append(leds, strings.NewReplacer("{color}", c, "{i}", strconv.Itoa(i)).Replace(p.Ring.LED))
		}
	}
	for i := 0; i < p.Ambilight.Pixels; i++ {
		for _, d := range p.Ambilight.Dies {
			for _, c := range p.Ambilight.Colors {
				leds = append(leds, strings.NewReplacer("{color}", c, "{i}", strconv.Itoa(i), "{die}", d).Replace(p.Ambilight.LED))
			}
		}
	}
	for _, l := range leds {
		d := filepath.Join(root, "sys/class/leds", l)
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
		for f, v := range map[string]string{"brightness": "0", "max_brightness": "255", "trigger": "[none] timer"} {
			if err := os.WriteFile(filepath.Join(d, f), []byte(v), 0o644); err != nil {
				return err
			}
		}
	}
	override := ""
	if p.Matrix.Kind == "dotmatrix-fb" {
		override = fmt.Sprintf("matrix.dev = virtual:%s\n", screen)
	}
	if err := os.WriteFile(filepath.Join(root, "board.conf"), []byte(override), 0o644); err != nil {
		return err
	}
	for _, d := range []string{config.CFWRoot(), p.Platform.CFWRoot, "/tmp"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			return err
		}
	}
	if patterns != "" && p.Clips.Dir != "" {
		if err := link(patterns, filepath.Join(root, p.Clips.Dir)); err != nil {
			return err
		}
	}
	if media != "" { // the default video_frames / video_audio live there
		if err := link(media, filepath.Join(root, config.CFWRoot(), "media")); err != nil {
			return err
		}
	}
	return nil
}

// link makes at a symlink to the local directory dir.
func link(dir, at string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
		return err
	}
	os.Remove(at)
	return os.Symlink(abs, at)
}
