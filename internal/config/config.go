// Package config charge la configuration du service depuis les variables
// d'environnement.
package config

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Config regroupe tous les paramètres runtime du backend.
type Config struct {
	PostgresHost     string
	PostgresPort     string
	PostgresDB       string
	PostgresUser     string
	PostgresPassword string

	ADDOKHost string

	// RoutingProvider est le fournisseur d'isochrones et d'itinéraires :
	// RoutingORS (OpenRouteService) ou RoutingValhalla (Valhalla auto-hébergé).
	RoutingProvider string
	// OpenRouteServiceKey et OpenRouteServiceURL configurent OpenRouteService
	// (URL vide : API publique).
	OpenRouteServiceKey string
	OpenRouteServiceURL string
	// ValhallaURL est l'URL du service Valhalla ; ValhallaConcurrency borne
	// ses requêtes d'isochrones simultanées.
	ValhallaURL         string
	ValhallaConcurrency int
	// SourcesParNature plafonne le nombre d'établissements sources par critère
	// d'une recherche (0 : pas de plafond). Défaut : 10 avec OpenRouteService
	// (quota de l'API publique), 0 avec Valhalla.
	SourcesParNature int
	// SearchCostWarningCents est le coût d'une recherche au-delà duquel
	// l'interface demande confirmation (0 : jamais).
	SearchCostWarningCents int64

	// JWTSecret signe et valide les JWT émis par le backend (auth native).
	JWTSecret string

	LogLevel string

	// PublicURL est l'URL publique du service, utilisée dans les liens envoyés par e-mail.
	PublicURL string

	// Serveur SMTP pour les e-mails de vérification. Si SMTPHost est vide, les
	// e-mails sont seulement journalisés (développement).
	SMTPHost     string
	SMTPPort     string
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string

	// ListenAddr est l'adresse d'écoute du serveur HTTP.
	ListenAddr string

	// Connexion OpenID Connect (optionnelle) : activée si OIDCIssuerURL est défini.
	OIDCIssuerURL    string
	OIDCClientID     string
	OIDCClientSecret string
	// OIDCProviderName est le libellé du bouton de connexion.
	OIDCProviderName string
	OIDCScopes       []string
	// OIDCRedirectURL est l'URL de retour enregistrée auprès du fournisseur.
	OIDCRedirectURL string
	// OIDCDefaultPerms est le niveau d'accès attribué aux comptes créés via OIDC.
	OIDCDefaultPerms string

	// Facturation prépayée (optionnelle) : activée si BillingEnabled.
	BillingEnabled bool
	// MollieAPIKey est la clé API Mollie : test_… (mode test) ou live_….
	MollieAPIKey string
	// MollieWebhookURL est l'URL notifiée par Mollie des changements de statut.
	MollieWebhookURL string

	// IsochroneCacheTTL est la durée de validité d'une isochrone en cache
	// (ISOCHRONE_CACHE_DAYS jours) ; au-delà, elle est recalculée.
	IsochroneCacheTTL time.Duration
	// IsochroneCacheMax est le nombre maximal d'isochrones en cache.
	IsochroneCacheMax int
}

// Fournisseurs de routage acceptés par ROUTING_PROVIDER.
const (
	RoutingORS      = "ors"
	RoutingValhalla = "valhalla"
)

// OIDCEnabled indique si la connexion OpenID Connect est configurée.
func (c Config) OIDCEnabled() bool { return c.OIDCIssuerURL != "" }

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
		OpenRouteServiceURL: strings.TrimSpace(env("OPENROUTESERVICE_URL", "")),
		RoutingProvider:     strings.ToLower(strings.TrimSpace(env("ROUTING_PROVIDER", RoutingORS))),
		ValhallaURL:         strings.TrimSpace(env("VALHALLA_URL", "http://localhost:8002")),
		JWTSecret:           env("JWT_SECRET", ""),
		LogLevel:            env("LOGLEVEL", "INFO"),
		ListenAddr:          env("LISTEN_ADDR", ":3566"),
		PublicURL:           strings.TrimRight(env("PUBLIC_URL", "http://localhost:3566"), "/"),
		SMTPHost:            env("SMTP_HOST", ""),
		SMTPPort:            env("SMTP_PORT", "587"),
		SMTPUsername:        env("SMTP_USERNAME", ""),
		SMTPPassword:        env("SMTP_PASSWORD", ""),
		SMTPFrom:            env("SMTP_FROM", ""),
		OIDCIssuerURL:       strings.TrimSpace(env("OIDC_ISSUER_URL", "")),
		OIDCClientID:        env("OIDC_CLIENT_ID", ""),
		OIDCClientSecret:    env("OIDC_CLIENT_SECRET", ""),
		OIDCProviderName:    env("OIDC_PROVIDER_NAME", "SSO"),
		OIDCScopes:          strings.Fields(env("OIDC_SCOPES", "openid email profile")),
		OIDCDefaultPerms:    strings.ToLower(env("OIDC_DEFAULT_PERMS", "verified")),
	}
	c.OIDCRedirectURL = env("OIDC_REDIRECT_URL", c.PublicURL+"/api/auth/oidc/callback")
	c.MollieAPIKey = strings.TrimSpace(env("MOLLIE_API_KEY", ""))
	c.MollieWebhookURL = env("MOLLIE_WEBHOOK_URL", c.PublicURL+"/api/billing/webhook/mollie")
	switch strings.ToLower(env("BILLING_ENABLED", "false")) {
	case "1", "true", "yes", "on":
		c.BillingEnabled = true
	}

	days, err := positiveInt("ISOCHRONE_CACHE_DAYS", 30)
	if err != nil {
		return Config{}, err
	}
	c.IsochroneCacheTTL = time.Duration(days) * 24 * time.Hour
	if c.IsochroneCacheMax, err = positiveInt("ISOCHRONE_CACHE_MAX", 20000); err != nil {
		return Config{}, err
	}

	if err := c.loadRouting(); err != nil {
		return Config{}, err
	}

	if c.JWTSecret == "" {
		return Config{}, fmt.Errorf("JWT_SECRET is required")
	}
	if c.SMTPHost != "" && c.SMTPFrom == "" {
		return Config{}, fmt.Errorf("SMTP_FROM is required when SMTP_HOST is set")
	}

	if c.OIDCEnabled() {
		if c.OIDCClientID == "" {
			return Config{}, fmt.Errorf("OIDC_CLIENT_ID is required when OIDC_ISSUER_URL is set")
		}
		switch c.OIDCDefaultPerms {
		case "unverified", "verified", "granted", "admin":
		default:
			return Config{}, fmt.Errorf("OIDC_DEFAULT_PERMS must be one of unverified, verified, granted, admin (got %q)", c.OIDCDefaultPerms)
		}
		if !slices.Contains(c.OIDCScopes, "openid") {
			c.OIDCScopes = append([]string{"openid"}, c.OIDCScopes...)
		}
	}

	if c.BillingEnabled && !strings.HasPrefix(c.MollieAPIKey, "test_") && !strings.HasPrefix(c.MollieAPIKey, "live_") {
		return Config{}, fmt.Errorf("MOLLIE_API_KEY (test_… or live_…) is required when BILLING_ENABLED is set")
	}

	return c, nil
}

// loadRouting lit et valide la configuration du routage et des recherches.
func (c *Config) loadRouting() error {
	switch c.RoutingProvider {
	case RoutingORS:
		c.SourcesParNature = 10
	case RoutingValhalla:
		c.SourcesParNature = 0
	default:
		return fmt.Errorf("ROUTING_PROVIDER must be %q or %q (got %q)", RoutingORS, RoutingValhalla, c.RoutingProvider)
	}
	for key, u := range map[string]string{"OPENROUTESERVICE_URL": c.OpenRouteServiceURL, "VALHALLA_URL": c.ValhallaURL} {
		if u != "" && !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			return fmt.Errorf("%s must be an http(s) URL (got %q)", key, u)
		}
	}
	var err error
	if c.ValhallaConcurrency, err = positiveInt("VALHALLA_CONCURRENCY", 8); err != nil {
		return err
	}
	if c.SourcesParNature, err = nonNegativeInt("SOURCES_PAR_NATURE", c.SourcesParNature); err != nil {
		return err
	}
	warn, err := nonNegativeInt("SEARCH_COST_WARNING_CENTS", 200)
	if err != nil {
		return err
	}
	c.SearchCostWarningCents = int64(warn)
	return nil
}

// nonNegativeInt lit un entier positif ou nul (def si la variable est absente).
func nonNegativeInt(key string, def int) (int, error) {
	v := env(key, "")
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer (got %q)", key, v)
	}
	return n, nil
}

// positiveInt lit un entier strictement positif (def si la variable est absente).
func positiveInt(key string, def int) (int, error) {
	v := env(key, "")
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer (got %q)", key, v)
	}
	return n, nil
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}
