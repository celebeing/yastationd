package main

import (
	"log/slog"
	"testing"
)

func TestParseLevel(t *testing.T) {
	for _, tt := range []struct {
		name string
		want slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"warn", slog.LevelWarn},
		{"error", slog.LevelError},
		{"info", slog.LevelInfo},
		{"unknown", slog.LevelInfo},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseLevel(tt.name); got != tt.want {
				t.Fatalf("parseLevel(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}
