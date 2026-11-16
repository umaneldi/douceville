package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testSecret = "super-secret-test-key"

func makeToken(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, err := tok.SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("signing token: %v", err)
	}
	return s
}

func validClaims() jwt.MapClaims {
	return jwt.MapClaims{
		"jti":   "token-id",
		"sub":   "user-123",
		"email": "alice@example.com",
		"aud":   "douceville",
		"exp":   time.Now().Add(time.Hour).Unix(),
	}
}

func TestParseToken_Valid(t *testing.T) {
	tok := makeToken(t, validClaims())

	claims, err := ParseToken(tok, testSecret)
	if err != nil {
		t.Fatalf("expected valid token, got error: %v", err)
	}
	if claims.Sub != "user-123" {
		t.Errorf("sub = %q, want user-123", claims.Sub)
	}
	if claims.Email != "alice@example.com" {
		t.Errorf("email = %q, want alice@example.com", claims.Email)
	}
}

func TestParseToken_Expired(t *testing.T) {
	c := validClaims()
	c["exp"] = time.Now().Add(-time.Hour).Unix()
	tok := makeToken(t, c)

	if _, err := ParseToken(tok, testSecret); err == nil {
		t.Fatal("expected error for expired token, got nil")
	}
}

func TestParseToken_WrongAudience(t *testing.T) {
	c := validClaims()
	c["aud"] = "anon"
	tok := makeToken(t, c)

	if _, err := ParseToken(tok, testSecret); err == nil {
		t.Fatal("expected error for wrong audience, got nil")
	}
}

func TestParseToken_BadSignature(t *testing.T) {
	tok := makeToken(t, validClaims())

	if _, err := ParseToken(tok, "a-different-secret"); err == nil {
		t.Fatal("expected error for bad signature, got nil")
	}
}

func TestParseToken_WrongSigningMethod(t *testing.T) {
	// Un token "alg: none" ne doit pas être accepté.
	tok := jwt.NewWithClaims(jwt.SigningMethodNone, validClaims())
	s, err := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("signing none token: %v", err)
	}

	if _, err := ParseToken(s, testSecret); err == nil {
		t.Fatal("expected error for alg=none token, got nil")
	}
}

func TestParseToken_MissingJTI(t *testing.T) {
	c := validClaims()
	delete(c, "jti")
	tok := makeToken(t, c)

	if _, err := ParseToken(tok, testSecret); err == nil {
		t.Fatal("expected error for missing jti, got nil")
	}
}

func TestParseToken_MissingExp(t *testing.T) {
	c := validClaims()
	delete(c, "exp")
	tok := makeToken(t, c)

	if _, err := ParseToken(tok, testSecret); err == nil {
		t.Fatal("expected error for missing exp, got nil")
	}
}
