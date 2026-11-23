// Package config charge la configuration du service depuis les variables
// d'environnement.
package config

import (
	"fmt"
	"os"
	"slices"
	"strings"
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
}

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
		JWTSecret:           env("JWT_SECRET", ""),
		LogLevel:            env("LOGLEVEL", "INFO"),
		ListenAddr:          env("LISTEN_ADDR", ":3566"),
		PublicURL:           strings.TrimRight(env("PUBLIC_URL", "http://localhost:3566"), "/"),
		SMTPHost:            env("SMTP_HOST", ""),
		SMTPPort:            env("SMTP_PORT", "587"),
		SMTPUsername:        env("SMTP_USERNAME", ""),
		SMTPPassword:        env("SMTP_PASSWORD", ""),
		SMTPFrom:            env("SMTP_FROM", ""),
		OIDCIssuerURL:       strings.TrimRight(env("OIDC_ISSUER_URL", ""), "/"),
		OIDCClientID:        env("OIDC_CLIENT_ID", ""),
		OIDCClientSecret:    env("OIDC_CLIENT_SECRET", ""),
		OIDCProviderName:    env("OIDC_PROVIDER_NAME", "SSO"),
		OIDCScopes:          strings.Fields(env("OIDC_SCOPES", "openid email profile")),
		OIDCDefaultPerms:    strings.ToLower(env("OIDC_DEFAULT_PERMS", "verified")),
	}
	c.OIDCRedirectURL = env("OIDC_REDIRECT_URL", c.PublicURL+"/api/auth/oidc/callback")

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

	return c, nil
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}
