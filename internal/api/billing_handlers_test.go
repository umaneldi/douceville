package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/umaneldi/douceville/internal/billing"
	"github.com/umaneldi/douceville/internal/models"
)

// billed renvoie des handlers avec facturation active, où u1 (alice) a balance
// centimes ; perms fixe son niveau.
func billed(t *testing.T, perms models.Perms, balance int64) (*Handlers, mockStore, *fakeProvider) {
	t.Helper()
	st := alice(t, true)
	st.user.Perms = perms
	st.user.BalanceCents = balance
	st.bill = newFakeBill(map[string]int64{"u1": balance, "u2": 0})
	st.zone = []models.Etablissement{{UAI: "A"}}
	prov := newFakeProvider()
	h := &Handlers{
		Store:             st,
		Isochron:          mockIso{iso: isoWithLevels(300, 600)},
		Geocoder:          mockGeo{},
		Billing:           prov,
		PublicURL:         "https://dv.example",
		BillingWebhookURL: "https://dv.example/api/billing/webhook/mollie",
	}
	return h, st, prov
}

const threeOrigins = `{"origines":[{"adresse":"a","dist":600},{"adresse":"b","dist":600},{"adresse":"c","dist":600}]}`

func TestBilling_ChargesPerOrigin(t *testing.T) {
	h, st, _ := billed(t, models.PermsGranted, 100)
	rr := call(t, h, http.MethodPost, "/api/etablissements", threeOrigins)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", rr.Code, rr.Body.String())
	}
	if got := st.bill.balances["u1"]; got != 85 {
		t.Errorf("balance = %d, want 85 (100 - 3×5)", got)
	}
	if rr.Header().Get("X-Credit-Balance") != "85" {
		t.Errorf("X-Credit-Balance = %q", rr.Header().Get("X-Credit-Balance"))
	}
}

func TestBilling_InsufficientCredit(t *testing.T) {
	h, st, _ := billed(t, models.PermsGranted, 14)
	rr := call(t, h, http.MethodPost, "/api/etablissements", threeOrigins)
	if rr.Code != http.StatusPaymentRequired || decodeMap(t, rr)["code"] != errCodeInsufficientCredit {
		t.Fatalf("status = %d (body: %s), want 402 insufficient_credit", rr.Code, rr.Body.String())
	}
	if st.bill.balances["u1"] != 14 || len(st.bill.txs) != 0 {
		t.Errorf("nothing must be debited: balance=%d txs=%v", st.bill.balances["u1"], st.bill.txs)
	}
}

func TestBilling_RefundOnFailure(t *testing.T) {
	h, st, _ := billed(t, models.PermsGranted, 100)
	h.Isochron = mockIso{err: errors.New("ORS down")}
	rr := call(t, h, http.MethodPost, "/api/etablissements", threeOrigins)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d", rr.Code)
	}
	if st.bill.balances["u1"] != 100 {
		t.Errorf("balance = %d, want 100 (refunded)", st.bill.balances["u1"])
	}
	if len(st.bill.txs) != 2 || st.bill.txs[1].Kind != models.CreditRefund {
		t.Errorf("txs = %+v, want usage then refund", st.bill.txs)
	}

	rr = call(t, h, http.MethodGet, "/api/isochrone?lat=43&lon=1&dist=600", "")
	if rr.Code != http.StatusBadGateway || st.bill.balances["u1"] != 100 {
		t.Errorf("isochrone: status = %d, balance = %d", rr.Code, st.bill.balances["u1"])
	}
}

func TestBilling_IsochronePrice(t *testing.T) {
	h, st, _ := billed(t, models.PermsGranted, 10)
	rr := call(t, h, http.MethodGet, "/api/isochrone?lat=43&lon=1&dist=600", "")
	if rr.Code != http.StatusOK || st.bill.balances["u1"] != 8 {
		t.Errorf("status = %d, balance = %d, want 200 and 8", rr.Code, st.bill.balances["u1"])
	}
}

func TestBilling_AdminAndDisabledAreFree(t *testing.T) {
	h, st, _ := billed(t, models.PermsAdmin, 0)
	if rr := call(t, h, http.MethodPost, "/api/etablissements", threeOrigins); rr.Code != http.StatusOK {
		t.Errorf("admin: status = %d", rr.Code)
	}
	h, st, _ = billed(t, models.PermsGranted, 0)
	h.Billing = nil
	if rr := call(t, h, http.MethodPost, "/api/etablissements", threeOrigins); rr.Code != http.StatusOK {
		t.Errorf("disabled: status = %d", rr.Code)
	}
	if len(st.bill.txs) != 0 {
		t.Errorf("txs = %v", st.bill.txs)
	}
}

func TestBilling_GetBilling(t *testing.T) {
	h, _, _ := billed(t, models.PermsGranted, 42)
	m := decodeMap(t, call(t, h, http.MethodGet, "/api/billing", ""))
	if m["enabled"] != true || m["mode"] != "test" || m["balance_cents"] != 42.0 ||
		m["price_per_origin_cents"] != 5.0 || m["free"] != false {
		t.Errorf("billing = %v", m)
	}
	h.Billing = nil
	if m := decodeMap(t, call(t, h, http.MethodGet, "/api/billing", "")); m["enabled"] != false {
		t.Errorf("disabled billing = %v", m)
	}
}

func webhook(h *Handlers, id string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/billing/webhook/mollie",
		strings.NewReader(url.Values{"id": {id}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	NewRouter(testSecret, h, h.Store.(mockStore)).ServeHTTP(rr, req)
	return rr
}

func TestBilling_TopupAndWebhook(t *testing.T) {
	h, st, prov := billed(t, models.PermsGranted, 0)

	if rr := call(t, h, http.MethodPost, "/api/billing/topup", `{"amount_cents":100}`); rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("below minimum: status = %d", rr.Code)
	}
	rr := call(t, h, http.MethodPost, "/api/billing/topup", `{"amount_cents":1000}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", rr.Code, rr.Body.String())
	}
	m := decodeMap(t, rr)
	id, _ := m["payment_id"].(string)
	if m["checkout_url"] != "https://pay.example/"+id || prov.last.RedirectURL != "https://dv.example/?billing_return=1" ||
		prov.last.Metadata["user_id"] != "u1" {
		t.Errorf("topup = %v, request = %+v", m, prov.last)
	}

	// Notification avant paiement : rien n'est crédité.
	if rr := webhook(h, id); rr.Code != http.StatusOK || st.bill.balances["u1"] != 0 {
		t.Errorf("open: status = %d, balance = %d", rr.Code, st.bill.balances["u1"])
	}
	// Paiement réussi, notifié deux fois : crédité une seule fois.
	prov.status[id] = billing.StatusPaid
	webhook(h, id)
	webhook(h, id)
	if st.bill.balances["u1"] != 1000 {
		t.Errorf("balance = %d, want 1000", st.bill.balances["u1"])
	}
	// Paiement inconnu : 200 sans effet.
	if rr := webhook(h, "tr_unknown"); rr.Code != http.StatusOK {
		t.Errorf("unknown: status = %d", rr.Code)
	}
}

func TestBilling_SyncAndFailedPayment(t *testing.T) {
	h, st, prov := billed(t, models.PermsGranted, 0)
	ok := decodeMap(t, call(t, h, http.MethodPost, "/api/billing/topup", `{"amount_cents":500}`))["payment_id"].(string)
	ko := decodeMap(t, call(t, h, http.MethodPost, "/api/billing/topup", `{"amount_cents":2000}`))["payment_id"].(string)
	prov.status[ok] = billing.StatusPaid
	prov.status[ko] = billing.StatusFailed

	for range 2 {
		if rr := call(t, h, http.MethodPost, "/api/billing/sync", ""); rr.Code != http.StatusOK {
			t.Fatalf("sync: status = %d", rr.Code)
		}
	}
	if st.bill.balances["u1"] != 500 || st.bill.payments[ko].Status != billing.StatusFailed {
		t.Errorf("balance = %d, failed payment = %+v", st.bill.balances["u1"], st.bill.payments[ko])
	}
}

func TestBilling_WebhookAmountMismatch(t *testing.T) {
	h, st, prov := billed(t, models.PermsGranted, 0)
	id := decodeMap(t, call(t, h, http.MethodPost, "/api/billing/topup", `{"amount_cents":500}`))["payment_id"].(string)
	prov.status[id] = billing.StatusPaid
	prov.amounts[id] = 1
	if rr := webhook(h, id); rr.Code != http.StatusInternalServerError || st.bill.balances["u1"] != 0 {
		t.Errorf("status = %d, balance = %d", rr.Code, st.bill.balances["u1"])
	}
}

func TestBilling_AdminRoutes(t *testing.T) {
	h, _, _ := billed(t, models.PermsGranted, 0)
	for _, rq := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/admin/billing/settings", ""},
		{http.MethodPut, "/api/admin/billing/settings", `{}`},
		{http.MethodPost, "/api/admin/users/u2/credit", `{"delta_cents":100,"reason":"x"}`},
		{http.MethodGet, "/api/admin/users/u2/transactions", ""},
	} {
		if rr := call(t, h, rq.method, rq.path, rq.body); rr.Code != http.StatusForbidden {
			t.Errorf("%s %s: status = %d, want 403", rq.method, rq.path, rr.Code)
		}
	}

	h, st, _ := billed(t, models.PermsAdmin, 0)
	rr := call(t, h, http.MethodPut, "/api/admin/billing/settings",
		`{"price_per_origin_cents":7,"isochrone_price_cents":1,"welcome_credit_cents":0,"min_topup_cents":1000}`)
	if rr.Code != http.StatusOK || st.bill.settings.PricePerOriginCents != 7 || st.bill.settings.MinTopupCents != 1000 {
		t.Errorf("update settings: status = %d, settings = %+v", rr.Code, st.bill.settings)
	}
	if rr := call(t, h, http.MethodPut, "/api/admin/billing/settings",
		`{"price_per_origin_cents":-1,"min_topup_cents":1000}`); rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("negative price: status = %d", rr.Code)
	}

	rr = call(t, h, http.MethodPost, "/api/admin/users/u2/credit", `{"delta_cents":250,"reason":"Geste commercial"}`)
	if rr.Code != http.StatusOK || decodeMap(t, rr)["balance_cents"] != 250.0 {
		t.Errorf("adjust: status = %d (body: %s)", rr.Code, rr.Body.String())
	}
	for body, want := range map[string]int{
		`{"delta_cents":-1000,"reason":"x"}`: http.StatusUnprocessableEntity, // solde négatif
		`{"delta_cents":100,"reason":" "}`:   http.StatusUnprocessableEntity, // motif requis
		`{"delta_cents":0,"reason":"x"}`:     http.StatusUnprocessableEntity,
	} {
		if rr := call(t, h, http.MethodPost, "/api/admin/users/u2/credit", body); rr.Code != want {
			t.Errorf("%s: status = %d, want %d", body, rr.Code, want)
		}
	}
	if rr := call(t, h, http.MethodPost, "/api/admin/users/nobody/credit", `{"delta_cents":1,"reason":"x"}`); rr.Code != http.StatusNotFound {
		t.Errorf("unknown user: status = %d", rr.Code)
	}
}
