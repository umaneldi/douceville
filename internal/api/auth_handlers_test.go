package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ydethe/douceville/internal/auth"
	"github.com/ydethe/douceville/internal/models"
	"github.com/ydethe/douceville/internal/store"
)

const testSecret = "unit-test-secret"

func TestRegister_Success(t *testing.T) {
	h := &Handlers{Store: mockStore{}, JWTSecret: testSecret}

	req := httptest.NewRequest(http.MethodPost, "/api/auth/register",
		strings.NewReader(`{"email":"Alice@Example.com","password":"supersecret"}`))
	rr := httptest.NewRecorder()
	h.register(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", rr.Code, rr.Body.String())
	}
	var got models.AuthResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Token == "" {
		t.Error("expected a token")
	}
	if _, err := auth.ParseToken(got.Token, testSecret); err != nil {
		t.Errorf("issued token should be valid: %v", err)
	}
}

func TestRegister_ShortPassword(t *testing.T) {
	h := &Handlers{Store: mockStore{}, JWTSecret: testSecret}
	req := httptest.NewRequest(http.MethodPost, "/api/auth/register",
		strings.NewReader(`{"email":"a@b.c","password":"short"}`))
	rr := httptest.NewRecorder()
	h.register(rr, req)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rr.Code)
	}
}

func TestRegister_DuplicateEmail(t *testing.T) {
	h := &Handlers{Store: mockStore{createErr: store.ErrEmailTaken}, JWTSecret: testSecret}
	req := httptest.NewRequest(http.MethodPost, "/api/auth/register",
		strings.NewReader(`{"email":"a@b.c","password":"supersecret"}`))
	rr := httptest.NewRecorder()
	h.register(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rr.Code)
	}
}

func TestLogin_Success(t *testing.T) {
	hash, _ := auth.HashPassword("supersecret")
	email := "alice@example.com"
	h := &Handlers{
		Store: mockStore{rec: &models.UserRecord{
			DvUser:       models.DvUser{ID: "u1", Email: &email},
			PasswordHash: hash,
		}},
		JWTSecret: testSecret,
	}

	req := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		strings.NewReader(`{"email":"alice@example.com","password":"supersecret"}`))
	rr := httptest.NewRecorder()
	h.login(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body.String())
	}
}

func TestLogin_WrongPassword(t *testing.T) {
	hash, _ := auth.HashPassword("supersecret")
	email := "alice@example.com"
	h := &Handlers{
		Store: mockStore{rec: &models.UserRecord{
			DvUser:       models.DvUser{ID: "u1", Email: &email},
			PasswordHash: hash,
		}},
		JWTSecret: testSecret,
	}
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		strings.NewReader(`{"email":"alice@example.com","password":"wrong"}`))
	rr := httptest.NewRecorder()
	h.login(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestLogin_UnknownUser(t *testing.T) {
	h := &Handlers{Store: mockStore{rec: nil}, JWTSecret: testSecret}
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		strings.NewReader(`{"email":"ghost@example.com","password":"whatever12"}`))
	rr := httptest.NewRecorder()
	h.login(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}
