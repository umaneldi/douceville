package main

import (
	"context"
	"log/slog"
	"testing"

	"github.com/umaneldi/douceville/internal/config"
	"github.com/umaneldi/douceville/internal/geo"
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

// newRouting choisit le client du fournisseur configuré.
func TestNewRouting(t *testing.T) {
	if _, ok := newRouting(config.Config{RoutingProvider: config.RoutingValhalla, ValhallaURL: "http://v:8002", ValhallaConcurrency: 2}).(*geo.ValhallaClient); !ok {
		t.Error("valhalla: want *geo.ValhallaClient")
	}
	for _, cfg := range []config.Config{
		{RoutingProvider: config.RoutingORS},
		{RoutingProvider: config.RoutingORS, OpenRouteServiceKey: "k", OpenRouteServiceURL: "https://ors.example"},
	} {
		if _, ok := newRouting(cfg).(*geo.ORSClient); !ok {
			t.Errorf("ors %+v: want *geo.ORSClient", cfg)
		}
	}
}
