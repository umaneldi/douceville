package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// VerificationTokenTTL est la durée de validité d'un lien de vérification d'e-mail.
const VerificationTokenTTL = 24 * time.Hour

// NewVerificationToken génère un jeton de vérification d'e-mail aléatoire
// (à envoyer à l'utilisateur) et son hash (à stocker en base).
func NewVerificationToken() (token, hash string, err error) {
	token, err = randomToken(32)
	if err != nil {
		return "", "", err
	}
	return token, HashVerificationToken(token), nil
}

// HashVerificationToken renvoie le hash SHA-256 (hexadécimal) d'un jeton de
// vérification. Seul ce hash est persisté, pour qu'une fuite de la base ne
// permette pas de valider des comptes.
func HashVerificationToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
