package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/umaneldi/douceville/internal/auth"
	"github.com/umaneldi/douceville/internal/models"
	"github.com/umaneldi/douceville/internal/store"
)

const testSecret = "unit-test-secret"

type sentMail struct{ to, subject, body string }

type fakeMailer struct{ sent *[]sentMail }

func (f fakeMailer) Send(ctx context.Context, to, subject, body string) error {
	*f.sent = append(*f.sent, sentMail{to, subject, body})
	return nil
}

func newMailer() (fakeMailer, *[]sentMail) {
	sent := &[]sentMail{}
	return fakeMailer{sent: sent}, sent
}

func TestRegister_SendsVerificationAndNoToken(t *testing.T) {
	mailer, sent := newMailer()
	h := &Handlers{Store: mockStore{}, JWTSecret: testSecret, Mailer: mailer, PublicURL: "https://dv.example"}

	req := httptest.NewRequest(http.MethodPost, "/api/auth/register",
		strings.NewReader(`{"email":"Alice@Example.com","password":"supersecret"}`))
	rr := httptest.NewRecorder()
	h.register(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", rr.Code, rr.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := got["token"]; ok {
		t.Error("no token must be issued before e-mail verification")
	}
	if len(*sent) != 1 {
		t.Fatalf("sent %d e-mails, want 1", len(*sent))
	}
	m := (*sent)[0]
	if m.to != "alice@example.com" {
		t.Errorf("to = %q, want alice@example.com", m.to)
	}
	if !strings.Contains(m.body, "https://dv.example/verify-email?token=") {
		t.Errorf("body does not contain verification link: %s", m.body)
	}
}

func TestRegister_InvalidEmail(t *testing.T) {
	for _, email := range []string{"", "nope", "a@b", "Bob <bob@example.com>", "@example.com"} {
		mailer, _ := newMailer()
		h := &Handlers{Store: mockStore{}, JWTSecret: testSecret, Mailer: mailer}
		body, _ := json.Marshal(map[string]string{"email": email, "password": "supersecret"})
		req := httptest.NewRequest(http.MethodPost, "/api/auth/register", strings.NewReader(string(body)))
		rr := httptest.NewRecorder()
		h.register(rr, req)
		if rr.Code != http.StatusUnprocessableEntity {
			t.Errorf("email %q: status = %d, want 422", email, rr.Code)
		}
	}
}

func TestLogin_RefusedBelowGranted(t *testing.T) {
	hash, _ := auth.HashPassword("supersecret")
	email := "alice@example.com"
	cases := []struct {
		perms models.Perms
		code  string
	}{
		{models.PermsUnverified, errCodeEmailNotVerified},
		{models.PermsVerified, auth.CodeAccountNotGranted},
	}
	for _, tc := range cases {
		h := &Handlers{
			Store: mockStore{rec: &models.UserRecord{
				DvUser:       models.DvUser{ID: "u1", Email: &email, Perms: tc.perms},
				PasswordHash: hash,
			}},
			JWTSecret: testSecret,
		}
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login",
			strings.NewReader(`{"email":"alice@example.com","password":"supersecret"}`))
		rr := httptest.NewRecorder()
		h.login(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("%s: status = %d, want 403", tc.perms, rr.Code)
		}
		if strings.Contains(rr.Body.String(), "token") {
			t.Errorf("%s: no token must be issued", tc.perms)
		}
		if !strings.Contains(rr.Body.String(), tc.code) {
			t.Errorf("%s: body = %s, want code %s", tc.perms, rr.Body.String(), tc.code)
		}
	}
}

func TestLogin_Admin(t *testing.T) {
	hash, _ := auth.HashPassword("supersecret")
	email := "alice@example.com"
	h := &Handlers{
		Store: mockStore{rec: &models.UserRecord{
			DvUser:       models.DvUser{ID: "u1", Email: &email, Perms: models.PermsAdmin},
			PasswordHash: hash,
		}},
		JWTSecret: testSecret,
	}
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		strings.NewReader(`{"email":"alice@example.com","password":"supersecret"}`))
	rr := httptest.NewRecorder()
	h.login(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
}

func TestVerifyEmail_AwaitingApproval(t *testing.T) {
	email := "alice@example.com"
	h := &Handlers{Store: mockStore{verified: &models.DvUser{ID: "u1", Email: &email, Perms: models.PermsVerified}}, JWTSecret: testSecret}
	req := httptest.NewRequest(http.MethodPost, "/api/auth/verify", strings.NewReader(`{"token":"abc"}`))
	rr := httptest.NewRecorder()
	h.verifyEmail(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := got["token"]; ok {
		t.Error("no token must be issued to a non-granted account")
	}
	if got["perms"] != "verified" {
		t.Errorf("perms = %v, want verified", got["perms"])
	}
}

func TestVerifyEmail_AlreadyGranted(t *testing.T) {
	email := "alice@example.com"
	h := &Handlers{Store: mockStore{verified: &models.DvUser{ID: "u1", Email: &email, Perms: models.PermsGranted}}, JWTSecret: testSecret}
	req := httptest.NewRequest(http.MethodPost, "/api/auth/verify", strings.NewReader(`{"token":"abc"}`))
	rr := httptest.NewRecorder()
	h.verifyEmail(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body.String())
	}
	var got models.AuthResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, err := auth.ParseToken(got.Token, testSecret); err != nil {
		t.Errorf("issued token should be valid: %v", err)
	}
}

func TestVerifyInfo(t *testing.T) {
	email := "bob@example.com"
	h := &Handlers{Store: mockStore{verified: &models.DvUser{ID: "u2", Email: &email}}, JWTSecret: testSecret}
	rr := httptest.NewRecorder()
	h.verifyInfo(rr, httptest.NewRequest(http.MethodPost, "/api/auth/verify/info", strings.NewReader(`{"token":"abc"}`)))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body.String())
	}
	var got map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["user_id"] != "u2" || got["email"] != email {
		t.Errorf("got %v, want user_id=u2 email=%s", got, email)
	}

	h = &Handlers{Store: mockStore{}, JWTSecret: testSecret}
	rr = httptest.NewRecorder()
	h.verifyInfo(rr, httptest.NewRequest(http.MethodPost, "/api/auth/verify/info", strings.NewReader(`{"token":"abc"}`)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid token: status = %d, want 400", rr.Code)
	}
}

func TestVerifyEmail_InvalidToken(t *testing.T) {
	h := &Handlers{Store: mockStore{verified: nil}, JWTSecret: testSecret}
	req := httptest.NewRequest(http.MethodPost, "/api/auth/verify", strings.NewReader(`{"token":"abc"}`))
	rr := httptest.NewRecorder()
	h.verifyEmail(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestResendVerification(t *testing.T) {
	email := "alice@example.com"
	cases := []struct {
		name  string
		rec   *models.UserRecord
		mails int
	}{
		{"unknown", nil, 0},
		{"verified", &models.UserRecord{DvUser: models.DvUser{ID: "u1", Email: &email, Perms: models.PermsVerified}}, 0},
		{"granted", &models.UserRecord{DvUser: models.DvUser{ID: "u1", Email: &email, Perms: models.PermsGranted}}, 0},
		{"unverified", &models.UserRecord{DvUser: models.DvUser{ID: "u1", Email: &email, Perms: models.PermsUnverified}}, 1},
	}
	for _, tc := range cases {
		mailer, sent := newMailer()
		h := &Handlers{Store: mockStore{rec: tc.rec}, Mailer: mailer}
		req := httptest.NewRequest(http.MethodPost, "/api/auth/resend-verification",
			strings.NewReader(`{"email":"alice@example.com"}`))
		rr := httptest.NewRecorder()
		h.resendVerification(rr, req)
		if rr.Code != http.StatusAccepted {
			t.Errorf("%s: status = %d, want 202", tc.name, rr.Code)
		}
		if len(*sent) != tc.mails {
			t.Errorf("%s: sent %d e-mails, want %d", tc.name, len(*sent), tc.mails)
		}
	}
}

func TestLogout_RevokesToken(t *testing.T) {
	var revoked string
	email := "alice@example.com"
	users := mockStore{user: &models.DvUser{ID: "u1", Email: &email, Perms: models.PermsGranted}, revoked: &revoked}
	h := &Handlers{Store: users, JWTSecret: testSecret}
	router := NewRouter(testSecret, h, users)

	tok, err := auth.IssueToken("u1", email, testSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	claims, _ := auth.ParseToken(tok, testSecret)

	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body: %s)", rr.Code, rr.Body.String())
	}
	if revoked != claims.ID {
		t.Errorf("revoked jti = %q, want %q", revoked, claims.ID)
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
			DvUser:       models.DvUser{ID: "u1", Email: &email, Perms: models.PermsGranted},
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
