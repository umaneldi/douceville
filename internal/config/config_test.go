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
