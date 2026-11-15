package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ydethe/douceville/internal/models"
)

type fakeUsers struct {
	user *models.DvUser
	err  error
}

func (f fakeUsers) GetUser(ctx context.Context, id string) (*models.DvUser, error) {
	return f.user, f.err
}

func newHandler(users UserLookup) http.Handler {
	mw := Middleware(testSecret, users)
	return mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, _ := UserFromContext(r.Context())
		if u != nil {
			_, _ = w.Write([]byte(u.Login))
		}
		w.WriteHeader(http.StatusOK)
	}))
}

func doRequest(h http.Handler, header string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/user", nil)
	if header != "" {
		req.Header.Set("Authorization", header)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestMiddleware_NoHeader(t *testing.T) {
	h := newHandler(fakeUsers{user: &models.DvUser{}})
	if code := doRequest(h, "").Code; code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", code)
	}
}

func TestMiddleware_WrongScheme(t *testing.T) {
	h := newHandler(fakeUsers{user: &models.DvUser{}})
	if code := doRequest(h, "Basic abc").Code; code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", code)
	}
}

func TestMiddleware_InvalidToken(t *testing.T) {
	h := newHandler(fakeUsers{user: &models.DvUser{}})
	if code := doRequest(h, "Bearer not-a-jwt").Code; code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", code)
	}
}

func TestMiddleware_UnknownUser(t *testing.T) {
	h := newHandler(fakeUsers{user: nil})
	tok := makeToken(t, validClaims())
	if code := doRequest(h, "Bearer "+tok).Code; code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", code)
	}
}

func TestMiddleware_ValidToken(t *testing.T) {
	h := newHandler(fakeUsers{user: &models.DvUser{ID: "user-123"}})
	tok := makeToken(t, validClaims())
	rr := doRequest(h, "Bearer "+tok)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	// Le login doit avoir été renseigné depuis le claim email.
	if body := rr.Body.String(); body != "alice@example.com" {
		t.Errorf("login = %q, want alice@example.com", body)
	}
}
