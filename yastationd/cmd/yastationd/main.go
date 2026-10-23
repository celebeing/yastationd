// Command yastationd is the on-device Home Assistant agent for the de-Yandexed Station Mini 3.
// Phase 1: LED ring, clock matrix, volume, mic-mute, button reflexes, announcements, health.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	_ "time/tzdata" // embed zoneinfo; the device has none

	"github.com/celebeing/yastationd/yastationd/internal/app"
	"github.com/celebeing/yastationd/yastationd/internal/config"
)

func main() {
	confPath := flag.String("config", config.CFWRoot()+"/yastationd.conf", "config file path")
	flag.Parse()

	cfg, err := config.Load(*confPath)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Warn("config not found, using defaults", "path", *confPath)
			cfg = config.Default()
		} else {
			slog.Error("load config", "err", err)
			os.Exit(1)
		}
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: parseLevel(cfg.LogLevel)}))
	slog.SetDefault(log)

	a, err := app.New(cfg, log)
	if err != nil {
		log.Error("init", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Info("yastationd starting", "device", cfg.DeviceID, "broker", cfg.MQTTHost)
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
