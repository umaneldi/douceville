package billing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAmounts(t *testing.T) {
	for cents, s := range map[int64]string{0: "0.00", 5: "0.05", 1000: "10.00", 1234: "12.34"} {
		if got := FormatAmount(cents); got != s {
			t.Errorf("FormatAmount(%d) = %q, want %q", cents, got, s)
		}
		if got, err := ParseAmount(s); err != nil || got != cents {
			t.Errorf("ParseAmount(%q) = %d, %v", s, got, err)
		}
	}
	if got, _ := ParseAmount("5.5"); got != 550 {
		t.Errorf("ParseAmount(5.5) = %d", got)
	}
	for _, bad := range []string{"", "abc", "1.234", "-1.00"} {
		if _, err := ParseAmount(bad); err == nil {
			t.Errorf("ParseAmount(%q): no error", bad)
		}
	}
}

func TestMollieClient(t *testing.T) {
	var created map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test_abc" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/payments":
			_ = json.NewDecoder(r.Body).Decode(&created)
			_, _ = w.Write([]byte(`{"id":"tr_1","status":"open","amount":{"currency":"EUR","value":"10.00"},
				"metadata":{"user_id":"u1"},"_links":{"checkout":{"href":"https://pay.example/tr_1"}}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/payments/tr_1":
			_, _ = w.Write([]byte(`{"id":"tr_1","status":"paid","amount":{"currency":"EUR","value":"10.00"},"metadata":{"user_id":"u1"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"title":"Not Found","detail":"No payment exists"}`))
		}
	}))
	defer srv.Close()

	c := NewMollieClient("test_abc", srv.URL, nil)
	if c.Mode() != "test" || NewMollieClient("live_x", "", nil).Mode() != "live" {
		t.Error("wrong mode")
	}
	ctx := context.Background()
	p, err := c.CreatePayment(ctx, PaymentRequest{AmountCents: 1000, Currency: "EUR", Description: "Recharge",
		RedirectURL: "http://localhost/", WebhookURL: "http://localhost/api/billing/webhook/mollie",
		Metadata: map[string]string{"user_id": "u1"}})
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "tr_1" || p.CheckoutURL != "https://pay.example/tr_1" || p.AmountCents != 1000 {
		t.Errorf("payment = %+v", p)
	}
	if amt := created["amount"].(map[string]any); amt["value"] != "10.00" || amt["currency"] != "EUR" {
		t.Errorf("amount sent = %v", amt)
	}
	if _, ok := created["webhookUrl"]; ok {
		t.Error("local webhookUrl must not be sent")
	}

	p, err = c.GetPayment(ctx, "tr_1")
	if err != nil || p.Status != StatusPaid || p.Metadata["user_id"] != "u1" {
		t.Errorf("GetPayment = %+v, %v", p, err)
	}
	if _, err := c.GetPayment(ctx, "tr_unknown"); err == nil {
		t.Error("expected error for unknown payment")
	}
}
