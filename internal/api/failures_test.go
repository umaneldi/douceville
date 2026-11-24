package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/umaneldi/douceville/internal/auth"
	"github.com/umaneldi/douceville/internal/billing"
	"github.com/umaneldi/douceville/internal/models"
)

var errDB = errors.New("database is down")

// failingStore délègue à mockStore, sauf la méthode nommée fail qui renvoie
// errDB. L'authentification (GetUser, IsTokenRevoked) n'échoue jamais : on
// peut ainsi atteindre les chemins d'erreur des routes protégées.
type failingStore struct {
	mockStore
	fail string
}

func (f failingStore) is(name string) bool { return f.fail == name }

func (f failingStore) ListUsers(ctx context.Context) ([]models.DvUser, error) {
	if f.is("ListUsers") {
		return nil, errDB
	}
	return f.mockStore.ListUsers(ctx)
}
func (f failingStore) SetUserPerms(ctx context.Context, id string, p models.Perms) (*models.DvUser, models.Perms, error) {
	if f.is("SetUserPerms") {
		return nil, "", errDB
	}
	return f.mockStore.SetUserPerms(ctx, id, p)
}
func (f failingStore) DeleteUser(ctx context.Context, id string) (bool, error) {
	if f.is("DeleteUser") {
		return false, errDB
	}
	return f.mockStore.DeleteUser(ctx, id)
}
func (f failingStore) GetBillingSettings(ctx context.Context) (models.BillingSettings, error) {
	if f.is("GetBillingSettings") {
		return models.BillingSettings{}, errDB
	}
	return f.mockStore.GetBillingSettings(ctx)
}
func (f failingStore) UpdateBillingSettings(ctx context.Context, s models.BillingSettings, adminID string) (models.BillingSettings, error) {
	if f.is("UpdateBillingSettings") {
		return models.BillingSettings{}, errDB
	}
	return f.mockStore.UpdateBillingSettings(ctx, s, adminID)
}
func (f failingStore) DebitCredit(ctx context.Context, userID string, cents int64, desc string) (int64, error) {
	if f.is("DebitCredit") {
		return 0, errDB
	}
	return f.mockStore.DebitCredit(ctx, userID, cents, desc)
}
func (f failingStore) AdjustCredit(ctx context.Context, userID string, delta int64, adminID, reason string) (int64, error) {
	if f.is("AdjustCredit") {
		return 0, errDB
	}
	return f.mockStore.AdjustCredit(ctx, userID, delta, adminID, reason)
}
func (f failingStore) ListCreditTransactions(ctx context.Context, userID string, limit int) ([]models.CreditTransaction, error) {
	if f.is("ListCreditTransactions") {
		return nil, errDB
	}
	return f.mockStore.ListCreditTransactions(ctx, userID, limit)
}
func (f failingStore) CreatePayment(ctx context.Context, p models.Payment) error {
	if f.is("CreatePayment") {
		return errDB
	}
	return f.mockStore.CreatePayment(ctx, p)
}
func (f failingStore) GetPayment(ctx context.Context, id string) (*models.Payment, error) {
	if f.is("GetPayment") {
		return nil, errDB
	}
	return f.mockStore.GetPayment(ctx, id)
}
func (f failingStore) ListPendingPayments(ctx context.Context, userID string) ([]models.Payment, error) {
	if f.is("ListPendingPayments") {
		return nil, errDB
	}
	return f.mockStore.ListPendingPayments(ctx, userID)
}
func (f failingStore) RevokeToken(ctx context.Context, jti string, exp time.Time) error {
	if f.is("RevokeToken") {
		return errDB
	}
	return f.mockStore.RevokeToken(ctx, jti, exp)
}
func (f failingStore) GetUserByEmail(ctx context.Context, email string) (*models.UserRecord, error) {
	if f.is("GetUserByEmail") {
		return nil, errDB
	}
	return f.mockStore.GetUserByEmail(ctx, email)
}
func (f failingStore) GetUserRecordByID(ctx context.Context, id string) (*models.UserRecord, error) {
	if f.is("GetUserRecordByID") {
		return nil, errDB
	}
	return f.mockStore.GetUserRecordByID(ctx, id)
}
func (f failingStore) CreateUser(ctx context.Context, email, hash string, fn, ln *string) (*models.DvUser, error) {
	if f.is("CreateUser") {
		return nil, errDB
	}
	return f.mockStore.CreateUser(ctx, email, hash, fn, ln)
}
func (f failingStore) PeekEmailVerification(ctx context.Context, tokenHash string) (string, string, bool, error) {
	if f.is("PeekEmailVerification") {
		return "", "", false, errDB
	}
	return f.mockStore.PeekEmailVerification(ctx, tokenHash)
}
func (f failingStore) VerifyEmail(ctx context.Context, tokenHash string) (*models.DvUser, bool, error) {
	if f.is("VerifyEmail") {
		return nil, false, errDB
	}
	return f.mockStore.VerifyEmail(ctx, tokenHash)
}
func (f failingStore) UpdateProfile(ctx context.Context, id string, p models.ProfileUpdate) (*models.DvUser, error) {
	if f.is("UpdateProfile") {
		return nil, errDB
	}
	return f.mockStore.UpdateProfile(ctx, id, p)
}
func (f failingStore) SetPassword(ctx context.Context, id, hash string) error {
	if f.is("SetPassword") {
		return errDB
	}
	return f.mockStore.SetPassword(ctx, id, hash)
}
func (f failingStore) CreateEmailChange(ctx context.Context, userID, newEmail, tokenHash string, exp time.Time) error {
	if f.is("CreateEmailChange") {
		return errDB
	}
	return f.mockStore.CreateEmailChange(ctx, userID, newEmail, tokenHash, exp)
}
func (f failingStore) GetEtablissementByUAI(ctx context.Context, uai string) (*models.Etablissement, error) {
	if f.is("GetEtablissementByUAI") {
		return nil, errDB
	}
	return f.mockStore.GetEtablissementByUAI(ctx, uai)
}
func (f failingStore) FindEtablissementsInZone(ctx context.Context, wkt string, nature, secteur []string) ([]models.Etablissement, error) {
	if f.is("FindEtablissementsInZone") {
		return nil, errDB
	}
	return f.mockStore.FindEtablissementsInZone(ctx, wkt, nature, secteur)
}
func (f failingStore) FindCommunesInZone(ctx context.Context, wkt string) ([]models.CommuneSecurite, error) {
	if f.is("FindCommunesInZone") {
		return nil, errDB
	}
	return f.mockStore.FindCommunesInZone(ctx, wkt)
}
func (f failingStore) FindImmobilierInZone(ctx context.Context, wkt string, codes []string) (models.ImmobilierZone, error) {
	if f.is("FindImmobilierInZone") {
		return models.ImmobilierZone{}, errDB
	}
	return f.mockStore.FindImmobilierInZone(ctx, wkt, codes)
}
func (f failingStore) FindLogementsSociauxInZone(ctx context.Context, wkt string) ([]models.LogementSocialPosition, error) {
	if f.is("FindLogementsSociauxInZone") {
		return nil, errDB
	}
	return f.mockStore.FindLogementsSociauxInZone(ctx, wkt)
}
func (f failingStore) IntersectZones(ctx context.Context, wkts []string) (string, json.RawMessage, bool, error) {
	if f.is("IntersectZones") {
		return "", nil, true, errDB
	}
	return f.mockStore.IntersectZones(ctx, wkts)
}
func (f failingStore) PointLevels(ctx context.Context, lonlats [][2]float64, wkts []string) ([]int, error) {
	if f.is("PointLevels") {
		return nil, errDB
	}
	return f.mockStore.PointLevels(ctx, lonlats, wkts)
}

// callAs exécute une requête authentifiée (compte u1) à travers le routeur ;
// users sert à l'authentification, h.Store aux handlers.
func callAs(t *testing.T, h *Handlers, users auth.UserLookup, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	h.JWTSecret = testSecret
	tok, err := auth.IssueToken("u1", aliceEmail, testSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	if strings.HasPrefix(body, "id=") {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	rr := httptest.NewRecorder()
	NewRouter(testSecret, h, users).ServeHTTP(rr, req)
	return rr
}

// TestStoreFailures vérifie que chaque erreur de la base est convertie en une
// réponse d'erreur propre (jamais un 200 ni une panique).
func TestStoreFailures(t *testing.T) {
	const search = `{"adresse":"a","dist":600}`
	cases := []struct {
		fail         string
		perms        models.Perms
		method, path string
		body         string
		want         int
	}{
		{"ListUsers", models.PermsAdmin, http.MethodGet, "/api/admin/users", "", 500},
		{"SetUserPerms", models.PermsAdmin, http.MethodPatch, "/api/admin/users/u2", `{"perms":"granted"}`, 500},
		{"DeleteUser", models.PermsAdmin, http.MethodDelete, "/api/admin/users/u2", "", 500},
		{"GetBillingSettings", models.PermsAdmin, http.MethodGet, "/api/admin/billing/settings", "", 500},
		{"UpdateBillingSettings", models.PermsAdmin, http.MethodPut, "/api/admin/billing/settings", `{"min_topup_cents":500}`, 500},
		{"AdjustCredit", models.PermsAdmin, http.MethodPost, "/api/admin/users/u2/credit", `{"delta_cents":1,"reason":"x"}`, 500},
		{"ListCreditTransactions", models.PermsAdmin, http.MethodGet, "/api/admin/users/u2/transactions", "", 500},
		{"GetBillingSettings", models.PermsGranted, http.MethodGet, "/api/billing", "", 500},
		{"ListCreditTransactions", models.PermsGranted, http.MethodGet, "/api/billing", "", 500},
		{"GetBillingSettings", models.PermsGranted, http.MethodPost, "/api/billing/topup", `{"amount_cents":1000}`, 500},
		{"CreatePayment", models.PermsGranted, http.MethodPost, "/api/billing/topup", `{"amount_cents":1000}`, 500},
		{"ListPendingPayments", models.PermsGranted, http.MethodPost, "/api/billing/sync", "", 500},
		{"GetPayment", models.PermsGranted, http.MethodPost, "/api/billing/webhook/mollie", "id=tr_a", 500},
		{"GetBillingSettings", models.PermsGranted, http.MethodPost, "/api/etablissements", search, 500},
		{"DebitCredit", models.PermsGranted, http.MethodPost, "/api/etablissements", search, 500},
		{"IntersectZones", models.PermsGranted, http.MethodPost, "/api/etablissements", search, 500},
		{"FindEtablissementsInZone", models.PermsGranted, http.MethodPost, "/api/etablissements", search, 500},
		{"FindCommunesInZone", models.PermsGranted, http.MethodPost, "/api/etablissements", search, 500},
		{"FindImmobilierInZone", models.PermsGranted, http.MethodPost, "/api/etablissements", search, 500},
		{"FindLogementsSociauxInZone", models.PermsGranted, http.MethodPost, "/api/etablissements", search, 500},
		{"PointLevels", models.PermsGranted, http.MethodPost, "/api/etablissements", search, 500},
		{"GetEtablissementByUAI", models.PermsGranted, http.MethodGet, "/api/etablissement/A", "", 500},
		{"RevokeToken", models.PermsGranted, http.MethodPost, "/api/auth/logout", "", 500},
		{"UpdateProfile", models.PermsGranted, http.MethodPatch, "/api/user", `{"first_name":"A"}`, 500},
		{"GetUserRecordByID", models.PermsGranted, http.MethodPost, "/api/user/password", `{"current_password":"supersecret","new_password":"newsecret1"}`, 500},
		{"SetPassword", models.PermsGranted, http.MethodPost, "/api/user/password", `{"current_password":"supersecret","new_password":"newsecret1"}`, 500},
		{"GetUserByEmail", models.PermsGranted, http.MethodPost, "/api/user/email", `{"email":"new@example.com","current_password":"supersecret"}`, 500},
		{"CreateEmailChange", models.PermsGranted, http.MethodPost, "/api/user/email", `{"email":"new@example.com","current_password":"supersecret"}`, 500},
		{"CreateUser", "", http.MethodPost, "/api/auth/register", `{"email":"n@example.com","password":"longenough"}`, 500},
		{"GetUserByEmail", "", http.MethodPost, "/api/auth/resend-verification", `{"email":"n@example.com"}`, 500},
		{"GetUserByEmail", "", http.MethodPost, "/api/auth/login", `{"email":"n@example.com","password":"longenough"}`, 500},
		{"PeekEmailVerification", "", http.MethodPost, "/api/auth/verify/info", `{"token":"t"}`, 500},
		{"VerifyEmail", "", http.MethodPost, "/api/auth/verify", `{"token":"t"}`, 500},
	}
	for _, tc := range cases {
		t.Run(tc.fail+" "+tc.method+" "+tc.path, func(t *testing.T) {
			perms := tc.perms
			if perms == "" {
				perms = models.PermsGranted
			}
			h, st, prov := billed(t, perms, 1000)
			bob := bobEmail
			st.users = []models.DvUser{*st.user, {ID: "u2", Email: &bob, Perms: models.PermsVerified}}
			st.etab = &models.Etablissement{UAI: "A"}
			st.zone = []models.Etablissement{{UAI: "A", Position: json.RawMessage(`{"type":"Point","coordinates":[1,2]}`)}}
			mailer, _ := newMailer()
			h.Mailer = mailer
			if tc.fail == "GetPayment" { // un paiement connu, pour atteindre la lecture
				if _, err := prov.CreatePayment(context.Background(), billing.PaymentRequest{AmountCents: 500}); err != nil {
					t.Fatal(err)
				}
			}
			h.Store = failingStore{mockStore: st, fail: tc.fail}

			rr := callAs(t, h, st, tc.method, tc.path, tc.body)
			if rr.Code != tc.want {
				t.Errorf("status = %d, want %d (body: %s)", rr.Code, tc.want, rr.Body.String())
			}
		})
	}
}

// TestSearchRefundedOnStoreFailure : une recherche payante qui échoue après le
// débit est remboursée.
func TestSearchRefundedOnStoreFailure(t *testing.T) {
	h, st, _ := billed(t, models.PermsGranted, 100)
	h.Store = failingStore{mockStore: st, fail: "FindCommunesInZone"}
	if rr := callAs(t, h, st, http.MethodPost, "/api/etablissements", `{"adresse":"a","dist":600}`); rr.Code != 500 {
		t.Fatalf("status = %d", rr.Code)
	}
	if st.bill.balances["u1"] != 100 {
		t.Errorf("balance = %d, want 100 (refunded)", st.bill.balances["u1"])
	}
}

// failMailer échoue à chaque envoi.
type failMailer struct{}

func (failMailer) Send(ctx context.Context, to, subject, body string) error {
	return errors.New("smtp down")
}

func TestMailerFailures(t *testing.T) {
	// Inscription : le compte est créé même si l'e-mail ne part pas.
	h := &Handlers{Store: mockStore{}, Mailer: failMailer{}, PublicURL: "https://dv.example"}
	rr := callAs(t, h, mockStore{}, http.MethodPost, "/api/auth/register", `{"email":"n@example.com","password":"longenough"}`)
	if rr.Code != http.StatusCreated {
		t.Errorf("register: status = %d", rr.Code)
	}

	// Changement d'adresse : l'échec d'envoi est signalé.
	st := alice(t, true)
	h = &Handlers{Store: st, Mailer: failMailer{}, PublicURL: "https://dv.example"}
	rr = callAs(t, h, st, http.MethodPost, "/api/user/email", `{"email":"new@example.com","current_password":"supersecret"}`)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("email change: status = %d", rr.Code)
	}

	// Validation par un administrateur : l'échec d'envoi n'empêche pas le changement.
	st = adminStore(t, models.PermsVerified)
	h = &Handlers{Store: st, Mailer: failMailer{}, PublicURL: "https://dv.example"}
	rr = callAs(t, h, st, http.MethodPatch, "/api/admin/users/u2", `{"perms":"granted"}`)
	if rr.Code != http.StatusOK {
		t.Errorf("approval: status = %d", rr.Code)
	}
}

func TestBillingDisabledRoutes(t *testing.T) {
	st := alice(t, true)
	h := &Handlers{Store: st}
	for _, rq := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/billing/topup", `{"amount_cents":1000}`},
		{http.MethodPost, "/api/billing/sync", ""},
		{http.MethodPost, "/api/billing/webhook/mollie", "id=tr_a"},
	} {
		if rr := callAs(t, h, st, rq.method, rq.path, rq.body); rr.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", rq.path, rr.Code)
		}
	}
}

func TestBillingBadRequests(t *testing.T) {
	h, st, prov := billed(t, models.PermsAdmin, 0)
	for _, rq := range []struct {
		method, path, body string
		want               int
	}{
		{http.MethodPost, "/api/billing/topup", `{`, 400},
		{http.MethodPost, "/api/billing/topup", `{"amount_cents":100001}`, 422},
		{http.MethodPut, "/api/admin/billing/settings", `{`, 400},
		{http.MethodPut, "/api/admin/billing/settings", `{"min_topup_cents":50}`, 422},
		{http.MethodPost, "/api/admin/users/u2/credit", `{`, 400},
		{http.MethodPost, "/api/billing/webhook/mollie", "id=" + strings.Repeat("x", 5000), 400}, // corps > 4 Kio
	} {
		if rr := callAs(t, h, st, rq.method, rq.path, rq.body); rr.Code != rq.want {
			t.Errorf("%s %s %q: status = %d, want %d", rq.method, rq.path, rq.body, rr.Code, rq.want)
		}
	}

	// Prestataire en erreur : 502, rien n'est enregistré.
	h.Billing = errProvider{prov}
	if rr := callAs(t, h, st, http.MethodPost, "/api/billing/topup", `{"amount_cents":1000}`); rr.Code != http.StatusBadGateway {
		t.Errorf("provider error: status = %d", rr.Code)
	}
	if len(st.bill.payments) != 0 {
		t.Errorf("payments = %v", st.bill.payments)
	}
}

// errProvider est un prestataire dont la création de paiement échoue.
type errProvider struct{ *fakeProvider }

func (errProvider) CreatePayment(ctx context.Context, req billing.PaymentRequest) (billing.PaymentInfo, error) {
	return billing.PaymentInfo{}, errors.New("mollie unavailable")
}

func TestAdminReadRoutes(t *testing.T) {
	h, st, _ := billed(t, models.PermsAdmin, 0)
	rr := callAs(t, h, st, http.MethodGet, "/api/admin/billing/settings", "")
	if m := decodeMap(t, rr); rr.Code != http.StatusOK || m["price_per_origin_cents"] != 5.0 || m["currency"] != "EUR" {
		t.Errorf("settings: status = %d, body = %v", rr.Code, m)
	}

	if rr := callAs(t, h, st, http.MethodPost, "/api/admin/users/u2/credit", `{"delta_cents":250,"reason":"x"}`); rr.Code != http.StatusOK {
		t.Fatalf("adjust: status = %d", rr.Code)
	}
	rr = callAs(t, h, st, http.MethodGet, "/api/admin/users/u2/transactions", "")
	var txs []models.CreditTransaction
	if err := json.Unmarshal(rr.Body.Bytes(), &txs); err != nil || rr.Code != http.StatusOK || len(txs) != 1 ||
		txs[0].AmountCents != 250 || txs[0].Kind != models.CreditAdminAdjust {
		t.Errorf("transactions: status = %d, txs = %+v, err = %v", rr.Code, txs, err)
	}
}

func TestHandlersWithoutAuthenticatedUser(t *testing.T) {
	// Appel direct (hors middleware) : aucun utilisateur dans le contexte.
	h := &Handlers{Store: mockStore{}}
	for name, fn := range map[string]http.HandlerFunc{
		"getUser":       h.getUser,
		"logout":        h.logout,
		"updateProfile": h.updateProfile,
	} {
		rr := httptest.NewRecorder()
		fn(rr, httptest.NewRequest(http.MethodGet, "/", strings.NewReader("{}")))
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401", name, rr.Code)
		}
	}
}

func TestOIDCLoginNotConfigured(t *testing.T) {
	h := &Handlers{Store: mockStore{}}
	if rr := callAs(t, h, mockStore{}, http.MethodGet, "/api/auth/oidc/login", ""); rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestFormatPrice(t *testing.T) {
	for _, tc := range []struct {
		cents    int64
		currency string
		want     string
	}{
		{5, "EUR", "0,05 €"},
		{1234, "EUR", "12,34 €"},
		{1000, "USD", "10,00 USD"},
	} {
		if got := formatPrice(tc.cents, tc.currency); got != tc.want {
			t.Errorf("formatPrice(%d, %s) = %q, want %q", tc.cents, tc.currency, got, tc.want)
		}
	}
}
