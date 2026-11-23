package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/umaneldi/douceville/internal/auth"
	"github.com/umaneldi/douceville/internal/models"
	"github.com/umaneldi/douceville/internal/store"
)

type stubOIDC struct {
	id  models.OIDCIdentity
	err error
}

func (s stubOIDC) DisplayName() string        { return "Keycloak" }
func (s stubOIDC) NewUserPerms() models.Perms { return models.PermsVerified }
func (s stubOIDC) AuthURL(w http.ResponseWriter) (string, error) {
	return "https://idp.example/authorize?x=1", nil
}
func (s stubOIDC) Exchange(w http.ResponseWriter, r *http.Request) (models.OIDCIdentity, error) {
	return s.id, s.err
}

func runCallback(t *testing.T, h *Handlers) *url.URL {
	t.Helper()
	rr := httptest.NewRecorder()
	h.oidcCallback(rr, httptest.NewRequest(http.MethodGet, "/api/auth/oidc/callback?code=c&state=s", nil))
	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 (body: %s)", rr.Code, rr.Body.String())
	}
	loc, err := url.Parse(rr.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestOIDCCallback_GrantedIssuesToken(t *testing.T) {
	email := "alice@example.com"
	h := &Handlers{
		Store:     mockStore{oidcUser: &models.DvUser{ID: "u1", Email: &email, Perms: models.PermsGranted}},
		JWTSecret: testSecret,
		OIDC:      stubOIDC{id: models.OIDCIdentity{Issuer: "https://idp", Subject: "s1", Email: email}},
	}
	loc := runCallback(t, h)
	if loc.Path != "/" || !strings.HasPrefix(loc.Fragment, "oidc_token=") {
		t.Fatalf("Location = %s, want /#oidc_token=...", loc)
	}
	frag, _ := url.ParseQuery(loc.Fragment)
	claims, err := auth.ParseToken(frag.Get("oidc_token"), testSecret)
	if err != nil {
		t.Fatalf("issued token invalid: %v", err)
	}
	if claims.Sub != "u1" || claims.Email != email {
		t.Errorf("claims = %+v", claims)
	}
}

func TestOIDCCallback_Errors(t *testing.T) {
	cases := []struct {
		name string
		h    *Handlers
		want string
	}{
		{"not granted", &Handlers{
			Store: mockStore{oidcUser: &models.DvUser{ID: "u1", Perms: models.PermsVerified}}, OIDC: stubOIDC{},
		}, auth.CodeAccountNotGranted},
		{"email taken", &Handlers{
			Store: mockStore{oidcErr: store.ErrEmailTaken}, OIDC: stubOIDC{},
		}, oidcErrEmailTaken},
		{"store failure", &Handlers{
			Store: mockStore{oidcErr: errors.New("db down")}, OIDC: stubOIDC{},
		}, oidcErrFailed},
		{"exchange failure", &Handlers{
			Store: mockStore{}, OIDC: stubOIDC{err: errors.New("state mismatch")},
		}, oidcErrFailed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.h.JWTSecret = testSecret
			loc := runCallback(t, c.h)
			if got := loc.Query().Get("oidc_error"); got != c.want || loc.Fragment != "" {
				t.Fatalf("Location = %s, want oidc_error=%s", loc, c.want)
			}
		})
	}
}

func TestOIDCConfigAndLogin(t *testing.T) {
	// Désactivé.
	h := &Handlers{}
	rr := httptest.NewRecorder()
	h.oidcConfig(rr, httptest.NewRequest(http.MethodGet, "/api/auth/oidc/config", nil))
	var got map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if got["enabled"] != false {
		t.Errorf("config (disabled) = %v", got)
	}
	rr = httptest.NewRecorder()
	h.oidcLogin(rr, httptest.NewRequest(http.MethodGet, "/api/auth/oidc/login", nil))
	if rr.Code != http.StatusNotFound {
		t.Errorf("login (disabled) status = %d, want 404", rr.Code)
	}

	// Activé.
	h = &Handlers{OIDC: stubOIDC{}}
	rr = httptest.NewRecorder()
	h.oidcConfig(rr, httptest.NewRequest(http.MethodGet, "/api/auth/oidc/config", nil))
	got = nil
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if got["enabled"] != true || got["name"] != "Keycloak" {
		t.Errorf("config (enabled) = %v", got)
	}
	rr = httptest.NewRecorder()
	h.oidcLogin(rr, httptest.NewRequest(http.MethodGet, "/api/auth/oidc/login", nil))
	if rr.Code != http.StatusFound || !strings.HasPrefix(rr.Header().Get("Location"), "https://idp.example/") {
		t.Errorf("login (enabled) = %d %s", rr.Code, rr.Header().Get("Location"))
	}
}
