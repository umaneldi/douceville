package config

import (
	"slices"
	"strings"
	"testing"
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
