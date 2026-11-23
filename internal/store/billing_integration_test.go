package store

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/umaneldi/douceville/internal/models"
)

// TestBillingIntegration exerce les requêtes de facturation sur une base
// PostGIS jetable : DV_TEST_DSN=postgres://…/base_vide go test ./internal/store/
// (ignoré si la variable n'est pas définie ; la base doit être vide).
func TestBillingIntegration(t *testing.T) {
	dsn := os.Getenv("DV_TEST_DSN")
	if dsn == "" {
		t.Skip("DV_TEST_DSN not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	s := New(pool)
	for range 2 { // migrations rejouables
		if err := s.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}

	u, err := s.CreateUser(ctx, "x@test.invalid", "hash", nil, nil)
	if err != nil || u.BalanceCents != 100 {
		t.Fatalf("create: %+v %v (welcome credit expected)", u, err)
	}
	o, err := s.FindOrCreateOIDCUser(ctx, models.OIDCIdentity{Issuer: "i", Subject: "s", Email: "o@test.invalid"}, models.PermsVerified)
	if err != nil || o.BalanceCents != 100 {
		t.Fatalf("oidc: %+v %v", o, err)
	}
	if b, err := s.DebitCredit(ctx, u.ID, 30, "r"); err != nil || b != 70 {
		t.Fatalf("debit: %d %v", b, err)
	}
	if _, err := s.DebitCredit(ctx, u.ID, 71, "r"); !errors.Is(err, ErrInsufficientCredit) {
		t.Fatalf("overdraft: %v", err)
	}
	if b, _ := s.RefundCredit(ctx, u.ID, 30, "r"); b != 100 {
		t.Fatalf("refund: %d", b)
	}
	if _, err := s.AdjustCredit(ctx, "nobody", 5, u.ID, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("adjust unknown: %v", err)
	}

	if err := s.CreatePayment(ctx, models.Payment{ID: "tr_1", UserID: u.ID, AmountCents: 500, Currency: "EUR", Status: "open", Mode: "test"}); err != nil {
		t.Fatal(err)
	}
	if ps, _ := s.ListPendingPayments(ctx, u.ID); len(ps) != 1 {
		t.Fatalf("pending: %v", ps)
	}
	c1, err1 := s.MarkPaymentPaid(ctx, "tr_1")
	c2, err2 := s.MarkPaymentPaid(ctx, "tr_1")
	if !c1 || c2 || err1 != nil || err2 != nil {
		t.Fatalf("mark paid twice: %v %v %v %v", c1, c2, err1, err2)
	}
	if err := s.UpdatePaymentStatus(ctx, "tr_1", "failed"); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.GetPayment(ctx, "tr_1"); p.Status != "paid" {
		t.Fatalf("paid payment status changed: %+v", p)
	}
	if got, _ := s.GetUser(ctx, u.ID); got.BalanceCents != 600 {
		t.Fatalf("balance = %d, want 600", got.BalanceCents)
	}
	if txs, _ := s.ListCreditTransactions(ctx, u.ID, 50); len(txs) != 4 || txs[0].Kind != models.CreditTopup {
		t.Fatalf("txs = %+v", txs)
	}

	st, err := s.UpdateBillingSettings(ctx, models.BillingSettings{PricePerOriginCents: 7, IsochronePriceCents: 1, MinTopupCents: 1000}, u.ID)
	if err != nil || st.PricePerOriginCents != 7 || st.Currency != "EUR" {
		t.Fatalf("settings: %+v %v", st, err)
	}
	if u2, _ := s.CreateUser(ctx, "y@test.invalid", "hash", nil, nil); u2.BalanceCents != 0 {
		t.Fatalf("welcome credit disabled, got %d", u2.BalanceCents)
	}
	if ok, err := s.DeleteUser(ctx, u.ID); !ok || err != nil {
		t.Fatalf("delete user with ledger rows: %v", err)
	}
}
