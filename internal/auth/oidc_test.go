package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"maps"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/umaneldi/douceville/internal/models"
)

// fakeIdP est un fournisseur OIDC minimal (discovery, JWKS, token endpoint).
type fakeIdP struct {
	srv       *httptest.Server
	key       *rsa.PrivateKey
	challenge string // code_challenge reçu à l'autorisation
	nonce     string // nonce à placer dans l'ID token
	claims    jwt.MapClaims
}

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIdP{key: key}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                f.srv.URL,
			"authorization_endpoint":                f.srv.URL + "/authorize",
			"token_endpoint":                        f.srv.URL + "/token",
			"jwks_uri":                              f.srv.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		b64 := base64.RawURLEncoding.EncodeToString
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "k1", "alg": "RS256", "use": "sig",
			"n": b64(key.N.Bytes()), "e": b64(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if r.Form.Get("code") != "good-code" || base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		claims := jwt.MapClaims{
			"iss": f.srv.URL, "sub": "user-123", "aud": "client-1",
			"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
			"nonce": f.nonce,
		}
		maps.Copy(claims, f.claims)
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tok.Header["kid"] = "k1"
		idToken, err := tok.SignedString(key)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at", "token_type": "Bearer", "expires_in": 3600, "id_token": idToken,
		})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// startFlow lance AuthURL et renvoie le cookie de flux et les paramètres d'autorisation.
func startFlow(t *testing.T, o *OIDC, f *fakeIdP) (*http.Cookie, url.Values) {
	t.Helper()
	rec := httptest.NewRecorder()
	u, err := o.AuthURL(rec)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(u, f.srv.URL+"/authorize") {
		t.Fatalf("unexpected auth URL %s", u)
	}
	q := parsed.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("nonce") == "" || q.Get("state") == "" {
		t.Fatalf("missing PKCE/nonce/state in %s", u)
	}
	f.challenge = q.Get("code_challenge")
	f.nonce = q.Get("nonce")
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != oidcCookie || !cookies[0].HttpOnly {
		t.Fatalf("unexpected cookies %+v", cookies)
	}
	return cookies[0], q
}

func callback(cookie *http.Cookie, state, code string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/auth/oidc/callback?state="+url.QueryEscape(state)+"&code="+code, nil)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	return r
}

func newTestOIDC(t *testing.T, f *fakeIdP) *OIDC {
	t.Helper()
	o, err := NewOIDC(context.Background(), OIDCConfig{
		IssuerURL: f.srv.URL, ClientID: "client-1", RedirectURL: "http://app/api/auth/oidc/callback",
		Scopes: []string{"openid", "email"}, Name: "Test", DefaultPerms: models.PermsVerified, Secret: "s3cret",
	})
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestOIDCRoundTrip(t *testing.T) {
	f := newFakeIdP(t)
	f.claims = jwt.MapClaims{"email": "Alice@Example.com", "email_verified": "true", "given_name": "Alice"}
	o := newTestOIDC(t, f)

	cookie, q := startFlow(t, o, f)
	id, err := o.Exchange(httptest.NewRecorder(), callback(cookie, q.Get("state"), "good-code"))
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	want := models.OIDCIdentity{Issuer: f.srv.URL, Subject: "user-123", Email: "alice@example.com", EmailVerified: true, GivenName: "Alice"}
	if id != want {
		t.Fatalf("identity = %+v, want %+v", id, want)
	}
}

func TestOIDCRejectsBadState(t *testing.T) {
	f := newFakeIdP(t)
	o := newTestOIDC(t, f)
	cookie, _ := startFlow(t, o, f)
	if _, err := o.Exchange(httptest.NewRecorder(), callback(cookie, "forged", "good-code")); err == nil {
		t.Fatal("expected state mismatch error")
	}
}

func TestOIDCRejectsMissingCookie(t *testing.T) {
	f := newFakeIdP(t)
	o := newTestOIDC(t, f)
	_, q := startFlow(t, o, f)
	if _, err := o.Exchange(httptest.NewRecorder(), callback(nil, q.Get("state"), "good-code")); err == nil {
		t.Fatal("expected missing cookie error")
	}
}

func TestOIDCRejectsBadNonce(t *testing.T) {
	f := newFakeIdP(t)
	o := newTestOIDC(t, f)
	cookie, q := startFlow(t, o, f)
	f.nonce = "other-nonce"
	if _, err := o.Exchange(httptest.NewRecorder(), callback(cookie, q.Get("state"), "good-code")); err == nil {
		t.Fatal("expected nonce mismatch error")
	}
}

func TestOIDCRejectsForgedCookie(t *testing.T) {
	f := newFakeIdP(t)
	o := newTestOIDC(t, f)
	_, q := startFlow(t, o, f)
	forged, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"aud": oidcAudience, "exp": time.Now().Add(time.Minute).Unix(),
		"state": q.Get("state"), "nonce": q.Get("nonce"), "pkce": "x",
	}).SignedString([]byte("wrong-secret"))
	c := &http.Cookie{Name: oidcCookie, Value: forged}
	if _, err := o.Exchange(httptest.NewRecorder(), callback(c, q.Get("state"), "good-code")); err == nil {
		t.Fatal("expected invalid cookie error")
	}
}
