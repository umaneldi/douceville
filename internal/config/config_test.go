package config

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestLoadOIDC(t *testing.T) {
	t.Setenv("JWT_SECRET", "x")
	t.Setenv("PUBLIC_URL", "https://dv.example/")

	c, err := Load()
	if err != nil || c.OIDCEnabled() {
		t.Fatalf("OIDC must be disabled by default (err=%v)", err)
	}

	t.Setenv("OIDC_ISSUER_URL", "https://idp.example/realms/r/")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "OIDC_CLIENT_ID") {
		t.Fatalf("missing client id: err = %v", err)
	}

	t.Setenv("OIDC_CLIENT_ID", "dv")
	t.Setenv("OIDC_SCOPES", "email profile")
	c, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.OIDCIssuerURL != "https://idp.example/realms/r/" ||
		c.OIDCRedirectURL != "https://dv.example/api/auth/oidc/callback" ||
		c.OIDCDefaultPerms != "verified" || c.OIDCProviderName != "SSO" ||
		!slices.Equal(c.OIDCScopes, []string{"openid", "email", "profile"}) {
		t.Fatalf("unexpected config %+v", c)
	}

	t.Setenv("OIDC_DEFAULT_PERMS", "superuser")
	if _, err := Load(); err == nil {
		t.Fatal("invalid OIDC_DEFAULT_PERMS must be rejected")
	}
}

func TestLoadBilling(t *testing.T) {
	t.Setenv("JWT_SECRET", "x")
	t.Setenv("PUBLIC_URL", "https://dv.example/")

	c, err := Load()
	if err != nil || c.BillingEnabled {
		t.Fatalf("billing must be disabled by default (err=%v)", err)
	}

	t.Setenv("BILLING_ENABLED", "true")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "MOLLIE_API_KEY") {
		t.Fatalf("missing key: err = %v", err)
	}
	t.Setenv("MOLLIE_API_KEY", "sk_wrong")
	if _, err := Load(); err == nil {
		t.Fatal("invalid key prefix must be rejected")
	}
	t.Setenv("MOLLIE_API_KEY", "test_abc")
	c, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if !c.BillingEnabled || c.MollieWebhookURL != "https://dv.example/api/billing/webhook/mollie" {
		t.Fatalf("unexpected config %+v", c)
	}
}

// clearEnv neutralise les variables lues par Load, pour que les tests ne
// dépendent pas de l'environnement du développeur.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"POSTGRES_HOST", "POSTGRES_PORT", "POSTGRES_DB", "POSTGRES_USER", "POSTGRES_PASSWORD",
		"ADDOK_HOST", "OPENROUTESERVICE_KEY", "JWT_SECRET", "LOGLEVEL", "LISTEN_ADDR", "PUBLIC_URL",
		"SMTP_HOST", "SMTP_PORT", "SMTP_USERNAME", "SMTP_PASSWORD", "SMTP_FROM",
		"OIDC_ISSUER_URL", "OIDC_CLIENT_ID", "OIDC_CLIENT_SECRET", "OIDC_PROVIDER_NAME", "OIDC_SCOPES",
		"OIDC_REDIRECT_URL", "OIDC_DEFAULT_PERMS", "BILLING_ENABLED", "MOLLIE_API_KEY", "MOLLIE_WEBHOOK_URL",
		"ISOCHRONE_CACHE_DAYS", "ISOCHRONE_CACHE_MAX",
		"ROUTING_PROVIDER", "OPENROUTESERVICE_URL", "VALHALLA_URL", "VALHALLA_CONCURRENCY",
		"SOURCES_PAR_NATURE", "SEARCH_COST_WARNING_CENTS",
	} {
		t.Setenv(k, "")
	}
}

func TestLoadDefaults(t *testing.T) {
	clearEnv(t)
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "JWT_SECRET") {
		t.Fatalf("missing JWT_SECRET: err = %v", err)
	}

	t.Setenv("JWT_SECRET", "s")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.ListenAddr != ":3566" || c.LogLevel != "INFO" || c.PublicURL != "http://localhost:3566" ||
		c.SMTPPort != "587" || c.BillingEnabled || c.OIDCEnabled() {
		t.Errorf("defaults = %+v", c)
	}
	if got := c.DatabaseURL(); got != "postgres://douceville:douceville@localhost:5432/douceville" {
		t.Errorf("DatabaseURL = %q", got)
	}
	if c.MollieWebhookURL != "http://localhost:3566/api/billing/webhook/mollie" ||
		c.OIDCRedirectURL != "http://localhost:3566/api/auth/oidc/callback" {
		t.Errorf("derived URLs: %q %q", c.MollieWebhookURL, c.OIDCRedirectURL)
	}

	t.Setenv("POSTGRES_HOST", "db")
	t.Setenv("POSTGRES_PORT", "6543")
	t.Setenv("POSTGRES_USER", "u")
	t.Setenv("POSTGRES_PASSWORD", "p")
	t.Setenv("POSTGRES_DB", "d")
	if c, _ := Load(); c.DatabaseURL() != "postgres://u:p@db:6543/d" {
		t.Errorf("DatabaseURL = %q", c.DatabaseURL())
	}
}

func TestLoadIsochroneCache(t *testing.T) {
	clearEnv(t)
	t.Setenv("JWT_SECRET", "s")
	c, err := Load()
	if err != nil || c.IsochroneCacheTTL != 30*24*time.Hour || c.IsochroneCacheMax != 20000 {
		t.Fatalf("defaults: ttl = %v, max = %d, err = %v; want 720h, 20000", c.IsochroneCacheTTL, c.IsochroneCacheMax, err)
	}

	t.Setenv("ISOCHRONE_CACHE_DAYS", " 7 ")
	t.Setenv("ISOCHRONE_CACHE_MAX", "500")
	if c, err := Load(); err != nil || c.IsochroneCacheTTL != 7*24*time.Hour || c.IsochroneCacheMax != 500 {
		t.Errorf("custom: ttl = %v, max = %d, err = %v", c.IsochroneCacheTTL, c.IsochroneCacheMax, err)
	}

	for key, v := range map[string]string{"ISOCHRONE_CACHE_DAYS": "0", "ISOCHRONE_CACHE_MAX": "beaucoup"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, v)
			if _, err := Load(); err == nil || !strings.Contains(err.Error(), key) {
				t.Errorf("%s=%q: err = %v, want an error naming the variable", key, v, err)
			}
		})
	}
}

func TestLoadSMTP(t *testing.T) {
	clearEnv(t)
	t.Setenv("JWT_SECRET", "s")
	t.Setenv("SMTP_HOST", "smtp.example.com")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "SMTP_FROM") {
		t.Fatalf("missing SMTP_FROM: err = %v", err)
	}
	t.Setenv("SMTP_FROM", "Douceville <no-reply@example.com>")
	if c, err := Load(); err != nil || c.SMTPHost != "smtp.example.com" || c.SMTPPort != "587" {
		t.Errorf("smtp = %+v, %v", c, err)
	}
}

func TestLoadRouting(t *testing.T) {
	clearEnv(t)
	t.Setenv("JWT_SECRET", "s")

	// Défauts : OpenRouteService public, plafond de 10 sources.
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.RoutingProvider != RoutingORS || c.OpenRouteServiceURL != "" || c.ValhallaURL != "http://localhost:8002" ||
		c.ValhallaConcurrency != 8 || c.SourcesParNature != 10 || c.SearchCostWarningCents != 200 {
		t.Errorf("defaults = provider %q, ors %q, valhalla %q (%d), sources %d, warning %d",
			c.RoutingProvider, c.OpenRouteServiceURL, c.ValhallaURL, c.ValhallaConcurrency, c.SourcesParNature, c.SearchCostWarningCents)
	}

	// Valhalla : pas de plafond par défaut.
	t.Setenv("ROUTING_PROVIDER", " Valhalla ")
	t.Setenv("VALHALLA_URL", "http://valhalla:8002")
	t.Setenv("VALHALLA_CONCURRENCY", "4")
	if c, err := Load(); err != nil || c.RoutingProvider != RoutingValhalla || c.SourcesParNature != 0 ||
		c.ValhallaURL != "http://valhalla:8002" || c.ValhallaConcurrency != 4 {
		t.Errorf("valhalla = %+v, %v; want no cap", c, err)
	}

	// Valeurs explicites, y compris 0 (pas de plafond, pas d'avertissement).
	t.Setenv("SOURCES_PAR_NATURE", "25")
	t.Setenv("SEARCH_COST_WARNING_CENTS", "0")
	t.Setenv("OPENROUTESERVICE_URL", "https://ors.example/ors")
	if c, err := Load(); err != nil || c.SourcesParNature != 25 || c.SearchCostWarningCents != 0 || c.OpenRouteServiceURL != "https://ors.example/ors" {
		t.Errorf("explicit = %+v, %v", c, err)
	}

	for _, tc := range []struct{ key, value string }{
		{"ROUTING_PROVIDER", "google"},
		{"VALHALLA_URL", "valhalla:8002"},
		{"OPENROUTESERVICE_URL", "ftp://ors"},
		{"VALHALLA_CONCURRENCY", "0"},
		{"SOURCES_PAR_NATURE", "-1"},
		{"SEARCH_COST_WARNING_CENTS", "cher"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			if _, err := Load(); err == nil || !strings.Contains(err.Error(), tc.key) {
				t.Errorf("%s=%q: err = %v, want an error naming the variable", tc.key, tc.value, err)
			}
		})
	}
}
