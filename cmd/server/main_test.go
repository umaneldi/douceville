package main

import (
	"context"
	"log/slog"
	"testing"
)

func TestSetupLogging(t *testing.T) {
	defer slog.SetDefault(slog.Default())
	for level, want := range map[string]slog.Level{
		"DEBUG":    slog.LevelDebug,
		" debug ":  slog.LevelDebug,
		"WARN":     slog.LevelWarn,
		"warning":  slog.LevelWarn,
		"ERROR":    slog.LevelError,
		"CRITICAL": slog.LevelError,
		"INFO":     slog.LevelInfo,
		"":         slog.LevelInfo,
		"bogus":    slog.LevelInfo,
	} {
		setupLogging(level)
		h := slog.Default().Handler()
		if !h.Enabled(context.Background(), want) || (want > slog.LevelDebug && h.Enabled(context.Background(), want-1)) {
			t.Errorf("setupLogging(%q): minimum level is not %v", level, want)
		}
	}
}
