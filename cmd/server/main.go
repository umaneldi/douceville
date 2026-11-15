// Command server est le point d'entrée du backend Douceville en Go.
package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ydethe/douceville/internal/api"
	"github.com/ydethe/douceville/internal/config"
	"github.com/ydethe/douceville/internal/geo"
	"github.com/ydethe/douceville/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx := context.Background()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL())
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("database ping: %v", err)
	}

	st := store.New(pool)
	if err := st.EnsureUserTable(ctx); err != nil {
		log.Fatalf("migration: %v", err)
	}

	handlers := &api.Handlers{
		Store:    st,
		Isochron: geo.NewORSClient(cfg.OpenRouteServiceKey, nil),
		Geocoder: geo.NewBANClient(cfg.ADDOKHost, nil),
	}

	router := api.NewRouter(cfg.APIPath, cfg.SupabaseJWTSecret, handlers, st)

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("Douceville API listening on %s (api_path=%q)", cfg.ListenAddr, cfg.APIPath)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server: %v", err)
	}
}
