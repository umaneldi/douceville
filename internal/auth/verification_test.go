package auth

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/umaneldi/douceville/internal/models"
)

func TestVerificationToken(t *testing.T) {
	tok, hash, err := NewVerificationToken()
	if err != nil {
		t.Fatal(err)
	}
	// 32 octets en base64url sans padding ; hash SHA-256 hexadécimal.
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`).MatchString(tok) {
		t.Errorf("token = %q", tok)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(hash) || hash != HashVerificationToken(tok) {
		t.Errorf("hash = %q", hash)
	}
	tok2, hash2, _ := NewVerificationToken()
	if tok2 == tok || hash2 == hash {
		t.Error("two tokens should differ")
	}
	// Valeur de référence : SHA-256("abc").
	if got := HashVerificationToken("abc"); got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Errorf("HashVerificationToken(abc) = %s", got)
	}
}

func TestContextAccessors(t *testing.T) {
	ctx := context.Background()
	if _, ok := UserFromContext(ctx); ok {
		t.Error("empty context: user found")
	}
	if _, ok := ClaimsFromContext(ctx); ok {
		t.Error("empty context: claims found")
	}

	c := Claims{Sub: "u1", ID: "jti", ExpiresAt: time.Unix(1, 0)}
	ctx = context.WithValue(context.WithValue(ctx, claimsCtxKey{}, c), ctxKey{}, &models.DvUser{ID: "u1"})
	if got, ok := ClaimsFromContext(ctx); !ok || got != c {
		t.Errorf("claims = %+v, %v", got, ok)
	}
	if got, ok := UserFromContext(ctx); !ok || got.ID != "u1" {
		t.Errorf("user = %+v, %v", got, ok)
	}
}

func TestOIDCAccessorsAndTruthy(t *testing.T) {
	o := &OIDC{Name: "Authentik", DefaultPerms: models.PermsGranted}
	if o.DisplayName() != "Authentik" || o.NewUserPerms() != models.PermsGranted {
		t.Errorf("accessors: %q %q", o.DisplayName(), o.NewUserPerms())
	}
	for v, want := range map[any]bool{true: true, false: false, "true": true, "TRUE": true, "false": false, "yes": false, 1: false} {
		if got := truthy(v); got != want {
			t.Errorf("truthy(%#v) = %v, want %v", v, got, want)
		}
	}
	if truthy(nil) {
		t.Error("truthy(nil) = true")
	}
}
