// Package auth gère l'authentification native par JWT porteur (Bearer) et le
// middleware associé. Les JWT sont émis et validés par le backend lui-même
// (HS256 avec le secret applicatif), puis le profil utilisateur est récupéré
// dans la table `utilisateur`.
package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Audience des tokens émis par Douceville.
const expectedAudience = "douceville"

// DefaultTokenTTL est la durée de validité par défaut d'un token émis.
const DefaultTokenTTL = 7 * 24 * time.Hour

// Claims extraits d'un token valide.
type Claims struct {
	Sub   string
	Email string
}

// IssueToken crée un JWT HS256 signé pour l'utilisateur donné.
func IssueToken(userID, email, secret string, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"sub":   userID,
		"email": email,
		"aud":   expectedAudience,
		"iat":   now.Unix(),
		"exp":   now.Add(ttl).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// ParseToken valide la signature HS256, l'expiration et l'audience du token,
// puis renvoie les claims `sub` et `email`.
func ParseToken(tokenString, secret string) (Claims, error) {
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{"HS256"}),
		jwt.WithAudience(expectedAudience),
		jwt.WithExpirationRequired(),
	)

	var mc jwt.MapClaims
	_, err := parser.ParseWithClaims(tokenString, &mc, func(t *jwt.Token) (any, error) {
		return []byte(secret), nil
	})
	if err != nil {
		return Claims{}, err
	}

	sub, err := mc.GetSubject()
	if err != nil || sub == "" {
		return Claims{}, errors.New("missing 'sub' claim")
	}

	email, _ := mc["email"].(string)

	return Claims{Sub: sub, Email: email}, nil
}
