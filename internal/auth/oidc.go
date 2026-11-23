package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/golang-jwt/jwt/v5"
	"github.com/umaneldi/douceville/internal/models"
	"golang.org/x/oauth2"
)

// oidcCookie est le cookie (JWT signé) qui conserve state, nonce et vérificateur
// PKCE entre la redirection vers le fournisseur et le retour (callback).
const (
	oidcCookie     = "dv_oidc"
	oidcCookiePath = "/api/auth/oidc"
	oidcCookieTTL  = 10 * time.Minute
	oidcAudience   = "douceville-oidc-flow"
)

// OIDCConfig regroupe les paramètres de connexion à un fournisseur OpenID Connect.
type OIDCConfig struct {
	IssuerURL    string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	Scopes       []string
	// Name est le libellé affiché sur le bouton de connexion.
	Name string
	// DefaultPerms est le niveau attribué aux comptes créés via OIDC.
	DefaultPerms models.Perms
	// Secret signe le cookie de flux (secret JWT applicatif).
	Secret string
	// SecureCookie positionne l'attribut Secure du cookie (service en HTTPS).
	SecureCookie bool
}

// OIDC implémente le flux « authorization code » (avec PKCE) d'OpenID Connect.
type OIDC struct {
	Name         string
	DefaultPerms models.Perms

	verifier     *oidc.IDTokenVerifier
	oauth        oauth2.Config
	secret       string
	secureCookie bool
}

// NewOIDC découvre la configuration du fournisseur (/.well-known/openid-configuration).
func NewOIDC(ctx context.Context, cfg OIDCConfig) (*OIDC, error) {
	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	provider, err := oidc.NewProvider(dctx, cfg.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	return &OIDC{
		Name:         cfg.Name,
		DefaultPerms: cfg.DefaultPerms,
		verifier:     provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		oauth: oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Endpoint:     provider.Endpoint(),
			Scopes:       cfg.Scopes,
		},
		secret:       cfg.Secret,
		secureCookie: cfg.SecureCookie,
	}, nil
}

// DisplayName renvoie le libellé du bouton de connexion.
func (o *OIDC) DisplayName() string { return o.Name }

// NewUserPerms renvoie le niveau attribué aux comptes créés via OIDC.
func (o *OIDC) NewUserPerms() models.Perms { return o.DefaultPerms }

// AuthURL prépare un nouveau flux : il dépose le cookie de flux et renvoie
// l'URL d'autorisation du fournisseur vers laquelle rediriger le navigateur.
func (o *OIDC) AuthURL(w http.ResponseWriter) (string, error) {
	state, err := randomToken(24)
	if err != nil {
		return "", err
	}
	nonce, err := randomToken(24)
	if err != nil {
		return "", err
	}
	verifier := oauth2.GenerateVerifier()

	cookie, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"aud":   oidcAudience,
		"exp":   time.Now().Add(oidcCookieTTL).Unix(),
		"state": state,
		"nonce": nonce,
		"pkce":  verifier,
	}).SignedString([]byte(o.secret))
	if err != nil {
		return "", err
	}
	o.setCookie(w, cookie, int(oidcCookieTTL.Seconds()))

	return o.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)), nil
}

// Exchange traite le retour du fournisseur : contrôle du state, échange du code
// (PKCE), vérification de l'ID token et de son nonce. Le cookie de flux est
// supprimé dans tous les cas.
func (o *OIDC) Exchange(w http.ResponseWriter, r *http.Request) (models.OIDCIdentity, error) {
	o.setCookie(w, "", -1)

	if e := r.URL.Query().Get("error"); e != "" {
		return models.OIDCIdentity{}, fmt.Errorf("provider error: %s: %s", e, r.URL.Query().Get("error_description"))
	}

	c, err := r.Cookie(oidcCookie)
	if err != nil {
		return models.OIDCIdentity{}, errors.New("missing flow cookie")
	}
	var mc jwt.MapClaims
	_, err = jwt.NewParser(
		jwt.WithValidMethods([]string{"HS256"}),
		jwt.WithAudience(oidcAudience),
		jwt.WithExpirationRequired(),
	).ParseWithClaims(c.Value, &mc, func(*jwt.Token) (any, error) { return []byte(o.secret), nil })
	if err != nil {
		return models.OIDCIdentity{}, fmt.Errorf("invalid flow cookie: %w", err)
	}
	state, _ := mc["state"].(string)
	nonce, _ := mc["nonce"].(string)
	verifier, _ := mc["pkce"].(string)
	if state == "" || state != r.URL.Query().Get("state") {
		return models.OIDCIdentity{}, errors.New("state mismatch")
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		return models.OIDCIdentity{}, errors.New("missing authorization code")
	}
	tok, err := o.oauth.Exchange(r.Context(), code, oauth2.VerifierOption(verifier))
	if err != nil {
		return models.OIDCIdentity{}, fmt.Errorf("code exchange: %w", err)
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		return models.OIDCIdentity{}, errors.New("no id_token in token response")
	}
	idt, err := o.verifier.Verify(r.Context(), raw)
	if err != nil {
		return models.OIDCIdentity{}, fmt.Errorf("id_token verification: %w", err)
	}
	if idt.Nonce != nonce {
		return models.OIDCIdentity{}, errors.New("nonce mismatch")
	}

	var claims struct {
		Email         string `json:"email"`
		EmailVerified any    `json:"email_verified"`
		GivenName     string `json:"given_name"`
		FamilyName    string `json:"family_name"`
		Picture       string `json:"picture"`
	}
	if err := idt.Claims(&claims); err != nil {
		return models.OIDCIdentity{}, fmt.Errorf("id_token claims: %w", err)
	}

	return models.OIDCIdentity{
		Issuer:        idt.Issuer,
		Subject:       idt.Subject,
		Email:         strings.TrimSpace(strings.ToLower(claims.Email)),
		EmailVerified: truthy(claims.EmailVerified),
		GivenName:     claims.GivenName,
		FamilyName:    claims.FamilyName,
		Picture:       claims.Picture,
	}, nil
}

func (o *OIDC) setCookie(w http.ResponseWriter, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     oidcCookie,
		Value:    value,
		Path:     oidcCookiePath,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   o.secureCookie,
		SameSite: http.SameSiteLaxMode,
	})
}

// truthy interprète email_verified, que certains fournisseurs envoient en chaîne.
func truthy(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return strings.EqualFold(t, "true")
	}
	return false
}
