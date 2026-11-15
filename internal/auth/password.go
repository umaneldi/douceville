package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Paramètres de dérivation PBKDF2.
const (
	pbkdf2Iterations = 210000 // recommandation OWASP pour PBKDF2-HMAC-SHA256
	pbkdf2KeyLen     = 32
	pbkdf2SaltLen    = 16
)

// ErrMismatchedPassword est renvoyée quand le mot de passe ne correspond pas.
var ErrMismatchedPassword = errors.New("password does not match")

// HashPassword dérive un hash PBKDF2-SHA256 salé, encodé sous la forme
// "pbkdf2_sha256$<iter>$<saltb64>$<hashb64>".
func HashPassword(password string) (string, error) {
	salt := make([]byte, pbkdf2SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	dk, err := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iterations, pbkdf2KeyLen)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"pbkdf2_sha256$%d$%s$%s",
		pbkdf2Iterations,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(dk),
	), nil
}

// VerifyPassword compare un mot de passe en clair au hash encodé stocké.
// Renvoie ErrMismatchedPassword si le mot de passe est incorrect.
func VerifyPassword(encoded, password string) error {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2_sha256" {
		return errors.New("invalid password hash format")
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil {
		return fmt.Errorf("invalid iteration count: %w", err)
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return fmt.Errorf("invalid salt: %w", err)
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return fmt.Errorf("invalid hash: %w", err)
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iter, len(want))
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return ErrMismatchedPassword
	}
	return nil
}
