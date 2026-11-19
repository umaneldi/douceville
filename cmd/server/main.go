// Command server est le point d'entrée du backend Douceville en Go.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/umaneldi/douceville/internal/api"
	"github.com/umaneldi/douceville/internal/config"
	"github.com/umaneldi/douceville/internal/geo"
	"github.com/umaneldi/douceville/internal/mail"
	"github.com/umaneldi/douceville/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fatal("config", err)
	}

	setupLogging(cfg.LogLevel)

	ctx := context.Background()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL())
	if err != nil {
		fatal("database", err)
	}
	defer pool.Close()

	pingCtx, cancelPing := context.WithTimeout(ctx, 10*time.Second)
	err = pool.Ping(pingCtx)
	cancelPing()
	if err != nil {
		fatal("database ping", err)
	}
	slog.Info("database connected", "host", cfg.PostgresHost, "port", cfg.PostgresPort, "db", cfg.PostgresDB)

	st := store.New(pool)
	canMigrate, err := st.CanMigrate(ctx)
	if err != nil {
		fatal("migration", err)
	}
	if canMigrate {
		if err := st.Migrate(ctx); err != nil {
			fatal("migration", err)
		}
		slog.Info("database migrations applied")
	} else {
		slog.Warn("database user is read-only: migrations skipped, the schema must already be up to date",
			"user", cfg.PostgresUser)
	}

	var mailer mail.Sender = mail.LogSender{}
	if cfg.SMTPHost != "" {
		mailer = mail.NewSMTPSender(mail.SMTPConfig{
			Host:     cfg.SMTPHost,
			Port:     cfg.SMTPPort,
			Username: cfg.SMTPUsername,
			Password: cfg.SMTPPassword,
			From:     cfg.SMTPFrom,
		})
		slog.Info("SMTP mailer configured", "host", cfg.SMTPHost, "port", cfg.SMTPPort)
	} else {
		slog.Warn("SMTP_HOST not set: verification e-mails will only be logged")
	}

	ban := geo.NewBANClient(cfg.ADDOKHost, nil)
	handlers := &api.Handlers{
		Store:     st,
		Isochron:  geo.NewORSClient(cfg.OpenRouteServiceKey, nil),
		Geocoder:  ban,
		Suggester: ban,
		JWTSecret: cfg.JWTSecret,
		Mailer:    mailer,
		PublicURL: cfg.PublicURL,
	}

	router := api.NewRouter(cfg.JWTSecret, handlers, st)

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          slog.NewLogLogger(slog.Default().Handler(), slog.LevelError),
	}

	slog.Info("Douceville listening (UI at /, API at /api)", "addr", cfg.ListenAddr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fatal("server", err)
	}
}

// setupLogging installe un logger slog texte sur stdout, au niveau demandé
// (DEBUG, INFO, WARN/WARNING, ERROR ; INFO par défaut).
func setupLogging(level string) {
	var lvl slog.Level
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "DEBUG":
		lvl = slog.LevelDebug
	case "WARN", "WARNING":
		lvl = slog.LevelWarn
	case "ERROR", "CRITICAL":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})))
	slog.Debug("logging configured", "level", lvl.String())
}

func fatal(step string, err error) {
	slog.Error(step+" failed", "error", err)
	os.Exit(1)
}
