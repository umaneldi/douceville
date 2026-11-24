package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// newTestStore crée une base PostGIS jetable (nom aléatoire) sur le serveur de
// DV_TEST_DSN, y applique les migrations (deux fois, pour vérifier qu'elles
// sont rejouables) et la supprime en fin de test. Le test est ignoré si
// DV_TEST_DSN n'est pas défini. Le rôle de DV_TEST_DSN doit pouvoir créer des
// bases (CREATEDB) et l'extension PostGIS.
//
//	DV_TEST_DSN=postgres://user:pass@localhost:5432/postgres go test ./internal/store/
func newTestStore(t *testing.T) (*PgStore, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("DV_TEST_DSN")
	if dsn == "" {
		t.Skip("DV_TEST_DSN not set")
	}
	ctx := context.Background()

	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect DV_TEST_DSN: %v", err)
	}
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	name := "dv_test_" + hex.EncodeToString(b[:])
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		admin.Close(ctx)
		t.Fatalf("create test database: %v", err)
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("drop test database %s: %v", name, err)
		}
		admin.Close(ctx)
	})

	s := New(pool)
	for range 2 { // migrations rejouables
		if err := s.Migrate(ctx); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}
	return s, pool
}

// mustExec exécute une requête de préparation des données de test.
func mustExec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}
