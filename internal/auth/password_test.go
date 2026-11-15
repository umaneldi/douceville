package auth

import (
	"strings"
	"testing"
)

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "pbkdf2_sha256$") {
		t.Errorf("unexpected hash format: %q", hash)
	}

	if err := VerifyPassword(hash, "correct horse battery staple"); err != nil {
		t.Errorf("VerifyPassword with correct password: %v", err)
	}

	if err := VerifyPassword(hash, "wrong password"); err != ErrMismatchedPassword {
		t.Errorf("VerifyPassword with wrong password = %v, want ErrMismatchedPassword", err)
	}
}

func TestHashPassword_Salted(t *testing.T) {
	h1, _ := HashPassword("same")
	h2, _ := HashPassword("same")
	if h1 == h2 {
		t.Error("expected different hashes for the same password (random salt)")
	}
}

func TestVerifyPassword_BadFormat(t *testing.T) {
	if err := VerifyPassword("not-a-valid-hash", "x"); err == nil {
		t.Error("expected error for malformed hash, got nil")
	}
}

func TestIssueAndParseToken(t *testing.T) {
	tok, err := IssueToken("user-123", "alice@example.com", testSecret, DefaultTokenTTL)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	claims, err := ParseToken(tok, testSecret)
	if err != nil {
		t.Fatalf("ParseToken: %v", err)
	}
	if claims.Sub != "user-123" || claims.Email != "alice@example.com" {
		t.Errorf("claims = %+v, want sub=user-123 email=alice@example.com", claims)
	}
}
