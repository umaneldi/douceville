package api

import (
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

const aliceEmail = "alice@example.com"

// alice renvoie un store simulant un compte connecté, avec ou sans mot de passe.
func alice(t *testing.T, withPassword bool) mockStore {
	t.Helper()
	email := aliceEmail
	u := models.DvUser{ID: "u1", Email: &email, Perms: models.PermsGranted, HasPassword: withPassword}
	rec := &models.UserRecord{DvUser: u}
	if withPassword {
		hash, err := auth.HashPassword("supersecret")
		if err != nil {
			t.Fatal(err)
		}
		rec.PasswordHash = hash
	}
	return mockStore{user: &u, byID: rec}
}

// call envoie une requête authentifiée (token de u1) à travers le routeur.
func call(t *testing.T, h *Handlers, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	h.JWTSecret = testSecret
	tok, err := auth.IssueToken("u1", "old-claim@example.com", testSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	NewRouter(testSecret, h, h.Store.(mockStore)).ServeHTTP(rr, req)
	return rr
}

func decodeMap(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode %q: %v", rr.Body.String(), err)
	}
	return m
}

func TestGetUser_LoginFollowsStoredEmail(t *testing.T) {
	rr := call(t, &Handlers{Store: alice(t, true)}, http.MethodGet, "/api/user", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if got := decodeMap(t, rr)["login"]; got != aliceEmail {
		t.Errorf("login = %v, want %s (stored e-mail, not the token claim)", got, aliceEmail)
	}
}

func TestUpdateProfile(t *testing.T) {
	h := &Handlers{Store: alice(t, true)}
	rr := call(t, h, http.MethodPatch, "/api/user",
		`{"first_name":"  Alice ","last_name":"","avatar_url":"https://img.example/a.png"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", rr.Code, rr.Body.String())
	}
	m := decodeMap(t, rr)
	if m["first_name"] != "Alice" || m["last_name"] != nil || m["avatar_url"] != "https://img.example/a.png" {
		t.Errorf("profile = %v", m)
	}

	for _, bad := range []string{
		`{"avatar_url":"javascript:alert(1)"}`,
		`{"avatar_url":"not a url"}`,
		`{"first_name":"` + strings.Repeat("x", 101) + `"}`,
	} {
		if rr := call(t, h, http.MethodPatch, "/api/user", bad); rr.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: status = %d, want 422", bad, rr.Code)
		}
	}
}

func TestChangeEmail(t *testing.T) {
	t.Run("success sends link to new address and notice to old one", func(t *testing.T) {
		var changed string
		st := alice(t, true)
		st.emailChange = &changed
		mailer, sent := newMailer()
		h := &Handlers{Store: st, Mailer: mailer, PublicURL: "https://dv.example"}
		rr := call(t, h, http.MethodPost, "/api/user/email", `{"email":" Bob@Example.com ","current_password":"supersecret"}`)
		if rr.Code != http.StatusAccepted {
			t.Fatalf("status = %d (body: %s)", rr.Code, rr.Body.String())
		}
		if changed != "bob@example.com" {
			t.Errorf("CreateEmailChange new_email = %q", changed)
		}
		if len(*sent) != 2 {
			t.Fatalf("sent %d e-mails, want 2", len(*sent))
		}
		if (*sent)[0].to != "bob@example.com" || !strings.Contains((*sent)[0].body, "https://dv.example/verify-email?token=") {
			t.Errorf("confirmation mail = %+v", (*sent)[0])
		}
		if (*sent)[1].to != aliceEmail || strings.Contains((*sent)[1].body, "verify-email") {
			t.Errorf("notice mail = %+v", (*sent)[1])
		}
	})

	t.Run("wrong password", func(t *testing.T) {
		mailer, sent := newMailer()
		rr := call(t, &Handlers{Store: alice(t, true), Mailer: mailer}, http.MethodPost, "/api/user/email",
			`{"email":"bob@example.com","current_password":"wrong"}`)
		if rr.Code != http.StatusForbidden || decodeMap(t, rr)["code"] != errCodeInvalidPassword {
			t.Fatalf("status = %d (body: %s), want 403 invalid_password", rr.Code, rr.Body.String())
		}
		if len(*sent) != 0 {
			t.Error("no e-mail must be sent")
		}
	})

	t.Run("address taken", func(t *testing.T) {
		st := alice(t, true)
		st.rec = &models.UserRecord{DvUser: models.DvUser{ID: "u2"}}
		mailer, _ := newMailer()
		rr := call(t, &Handlers{Store: st, Mailer: mailer}, http.MethodPost, "/api/user/email",
			`{"email":"bob@example.com","current_password":"supersecret"}`)
		if rr.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409", rr.Code)
		}
	})

	t.Run("same or invalid address", func(t *testing.T) {
		mailer, _ := newMailer()
		h := &Handlers{Store: alice(t, true), Mailer: mailer}
		for _, body := range []string{
			`{"email":"ALICE@example.com","current_password":"supersecret"}`,
			`{"email":"nope","current_password":"supersecret"}`,
		} {
			if rr := call(t, h, http.MethodPost, "/api/user/email", body); rr.Code != http.StatusUnprocessableEntity {
				t.Errorf("%s: status = %d, want 422", body, rr.Code)
			}
		}
	})

	t.Run("account without password", func(t *testing.T) {
		mailer, _ := newMailer()
		rr := call(t, &Handlers{Store: alice(t, false), Mailer: mailer}, http.MethodPost, "/api/user/email",
			`{"email":"bob@example.com"}`)
		if rr.Code != http.StatusAccepted {
			t.Fatalf("status = %d (body: %s), want 202", rr.Code, rr.Body.String())
		}
	})
}

func TestChangePassword(t *testing.T) {
	cases := []struct {
		name         string
		withPassword bool
		body         string
		want         int
	}{
		{"success", true, `{"current_password":"supersecret","new_password":"newsecret1"}`, http.StatusNoContent},
		{"wrong current", true, `{"current_password":"nope","new_password":"newsecret1"}`, http.StatusForbidden},
		{"too short", true, `{"current_password":"supersecret","new_password":"short"}`, http.StatusUnprocessableEntity},
		{"set first password (OIDC account)", false, `{"new_password":"newsecret1"}`, http.StatusNoContent},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var stored string
			st := alice(t, c.withPassword)
			st.setPassword = &stored
			rr := call(t, &Handlers{Store: st}, http.MethodPost, "/api/user/password", c.body)
			if rr.Code != c.want {
				t.Fatalf("status = %d (body: %s), want %d", rr.Code, rr.Body.String(), c.want)
			}
			if c.want == http.StatusNoContent && auth.VerifyPassword(stored, "newsecret1") != nil {
				t.Error("stored hash does not match the new password")
			}
			if c.want != http.StatusNoContent && stored != "" {
				t.Error("password must not be changed")
			}
		})
	}
}

func TestVerifyEmail_EmailChange(t *testing.T) {
	newEmail := "bob@example.com"
	h := &Handlers{
		Store:     mockStore{verified: &models.DvUser{ID: "u1", Email: &newEmail, Perms: models.PermsGranted}, emailChanged: true},
		JWTSecret: testSecret,
	}
	rr := httptest.NewRecorder()
	h.verifyEmail(rr, httptest.NewRequest(http.MethodPost, "/api/auth/verify", strings.NewReader(`{"token":"t"}`)))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	m := decodeMap(t, rr)
	if m["email_changed"] != true {
		t.Errorf("email_changed = %v", m["email_changed"])
	}
	claims, err := auth.ParseToken(m["token"].(string), testSecret)
	if err != nil || claims.Email != newEmail {
		t.Errorf("token claims = %+v (err %v), want e-mail %s", claims, err, newEmail)
	}

	h.Store = mockStore{err: store.ErrEmailTaken}
	rr = httptest.NewRecorder()
	h.verifyEmail(rr, httptest.NewRequest(http.MethodPost, "/api/auth/verify", strings.NewReader(`{"token":"t"}`)))
	if rr.Code != http.StatusConflict {
		t.Errorf("taken: status = %d, want 409", rr.Code)
	}
}
