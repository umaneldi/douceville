// Package config charge la configuration du service depuis les variables
// d'environnement (équivalent de src/douceville/config.py côté Python).
package config

import (
	"fmt"
	"os"
)

// Config regroupe tous les paramètres runtime du backend.
type Config struct {
	PostgresHost     string
	PostgresPort     string
	PostgresDB       string
	PostgresUser     string
	PostgresPassword string

	ADDOKHost           string
	OpenRouteServiceKey string
	SupabaseJWTSecret   string

	APIPath  string
	LogLevel string

	// ListenAddr est l'adresse d'écoute du serveur HTTP.
	ListenAddr string
}

// DatabaseURL construit le DSN PostgreSQL (format pgx/libpq).
func (c Config) DatabaseURL() string {
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s",
		c.PostgresUser, c.PostgresPassword, c.PostgresHost, c.PostgresPort, c.PostgresDB,
	)
}

// Load lit la configuration depuis l'environnement. Les variables indispensables
// manquantes provoquent une erreur ; les autres prennent une valeur par défaut.
func Load() (Config, error) {
	c := Config{
		PostgresHost:        env("POSTGRES_HOST", "localhost"),
		PostgresPort:        env("POSTGRES_PORT", "5432"),
		PostgresDB:          env("POSTGRES_DB", "douceville"),
		PostgresUser:        env("POSTGRES_USER", "douceville"),
		PostgresPassword:    env("POSTGRES_PASSWORD", "douceville"),
		ADDOKHost:           env("ADDOK_HOST", ""),
		OpenRouteServiceKey: env("OPENROUTESERVICE_KEY", ""),
		SupabaseJWTSecret:   env("SUPABASE_JWT_SECRET", ""),
		APIPath:             env("API_PATH", ""),
		LogLevel:            env("LOGLEVEL", "INFO"),
		ListenAddr:          env("LISTEN_ADDR", ":3566"),
	}

	if c.SupabaseJWTSecret == "" {
		return Config{}, fmt.Errorf("SUPABASE_JWT_SECRET is required")
	}

	return c, nil
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}
