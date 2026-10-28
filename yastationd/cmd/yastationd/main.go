// Command yastationd is the CFW's on-device agent: it turns a speaker into an offline Home
// Assistant speaker (LEDs and front display, keys, volume, wake word and voice satellite, music,
// intercom, web app). The board profile (internal/board) adapts it to each model; see README.md.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	_ "time/tzdata" // embed zoneinfo; the device has none

	"github.com/celebeing/yastationd/yastationd/internal/app"
	"github.com/celebeing/yastationd/yastationd/internal/config"
	"github.com/celebeing/yastationd/yastationd/internal/version"
)

func main() {
	confPath := flag.String("config", config.CFWRoot()+"/yastationd.conf", "config file path")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("yastationd", version.Version)
		return
	}

	cfg, skipped, err := config.LoadLenient(*confPath)
	for _, e := range skipped {
		slog.Error("config: line skipped, its key keeps the default", "path", *confPath, "err", e.Error())
	}
	if err != nil {
		if os.IsNotExist(err) {
			slog.Warn("config not found, using defaults", "path", *confPath)
			cfg = config.Default()
		} else {
			slog.Error("load config", "err", err)
			os.Exit(1)
		}
	}

	cfg.File = *confPath

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: parseLevel(cfg.LogLevel)}))
	slog.SetDefault(log)

	a, err := app.New(cfg, log)
	if err != nil {
		log.Error("init", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Info("yastationd starting", "version", version.Version, "device", cfg.DeviceID, "broker", cfg.MQTTHost)
	if err := a.Run(ctx); err != nil {
		log.Error("run", "err", err)
		os.Exit(1)
	}
	log.Info("yastationd stopped")
}

func parseLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
