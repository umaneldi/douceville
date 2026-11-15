// Package auth gère l'authentification par JWT porteur (Bearer) et le middleware
// associé. Le JWT est validé en HS256 avec le secret Supabase, puis le profil
// utilisateur est récupéré dans la table dédiée `utilisateur`.
package auth

import (
	"errors"

	"github.com/golang-jwt/jwt/v5"
)

// Audience attendue dans le claim `aud` des tokens Supabase.
const expectedAudience = "authenticated"

// Claims extraits d'un token valide.
type Claims struct {
	Sub   string
	Email string
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
