package api

import (
	"context"
	"errors"

	"github.com/umaneldi/douceville/internal/billing"
	"github.com/umaneldi/douceville/internal/models"
	"github.com/umaneldi/douceville/internal/store"
)

// fakeBill simule la facturation en mémoire (soldes, journal, paiements).
type fakeBill struct {
	settings models.BillingSettings
	balances map[string]int64
	txs      []models.CreditTransaction
	payments map[string]*models.Payment
	debitErr error
}

func newFakeBill(balances map[string]int64) *fakeBill {
	return &fakeBill{
		settings: models.BillingSettings{PricePerOriginCents: 5, IsochronePriceCents: 2, WelcomeCreditCents: 100,
			MinTopupCents: 500, Currency: "EUR"},
		balances: balances,
		payments: map[string]*models.Payment{},
	}
}

func (b *fakeBill) apply(userID string, delta int64, kind string) (int64, error) {
	bal, ok := b.balances[userID]
	if !ok {
		return 0, store.ErrNotFound
	}
	if bal+delta < 0 {
		return 0, store.ErrInsufficientCredit
	}
	b.balances[userID] = bal + delta
	b.txs = append(b.txs, models.CreditTransaction{ID: int64(len(b.txs) + 1), AmountCents: delta, Kind: kind})
	return bal + delta, nil
}

func (m mockStore) GetBillingSettings(ctx context.Context) (models.BillingSettings, error) {
	if m.bill == nil {
		return models.BillingSettings{Currency: "EUR"}, m.err
	}
	return m.bill.settings, m.err
}
func (m mockStore) UpdateBillingSettings(ctx context.Context, s models.BillingSettings, adminID string) (models.BillingSettings, error) {
	s.Currency = "EUR"
	if m.bill != nil {
		m.bill.settings = s
	}
	return s, m.err
}
func (m mockStore) DebitCredit(ctx context.Context, userID string, cents int64, desc string) (int64, error) {
	if m.bill == nil {
		return 0, errors.New("no billing state")
	}
	if m.bill.debitErr != nil {
		return 0, m.bill.debitErr
	}
	return m.bill.apply(userID, -cents, models.CreditUsage)
}
func (m mockStore) RefundCredit(ctx context.Context, userID string, cents int64, desc string) (int64, error) {
	return m.bill.apply(userID, cents, models.CreditRefund)
}
func (m mockStore) AdjustCredit(ctx context.Context, userID string, delta int64, adminID, reason string) (int64, error) {
	return m.bill.apply(userID, delta, models.CreditAdminAdjust)
}
func (m mockStore) ListCreditTransactions(ctx context.Context, userID string, limit int) ([]models.CreditTransaction, error) {
	if m.bill == nil {
		return []models.CreditTransaction{}, m.err
	}
	return append([]models.CreditTransaction{}, m.bill.txs...), m.err
}
func (m mockStore) CreatePayment(ctx context.Context, p models.Payment) error {
	m.bill.payments[p.ID] = &p
	return nil
}
func (m mockStore) GetPayment(ctx context.Context, id string) (*models.Payment, error) {
	if p, ok := m.bill.payments[id]; ok {
		cp := *p
		return &cp, nil
	}
	return nil, nil
}
func (m mockStore) MarkPaymentPaid(ctx context.Context, id string) (bool, error) {
	p, ok := m.bill.payments[id]
	if !ok || p.Status == billing.StatusPaid {
		return false, nil
	}
	p.Status = billing.StatusPaid
	_, err := m.bill.apply(p.UserID, p.AmountCents, models.CreditTopup)
	return err == nil, err
}
func (m mockStore) UpdatePaymentStatus(ctx context.Context, id, status string) error {
	if p, ok := m.bill.payments[id]; ok && p.Status != billing.StatusPaid {
		p.Status = status
	}
	return nil
}
func (m mockStore) ListPendingPayments(ctx context.Context, userID string) ([]models.Payment, error) {
	ps := []models.Payment{}
	for _, p := range m.bill.payments {
		if p.UserID == userID && p.Status != billing.StatusPaid && p.Status != billing.StatusFailed {
			ps = append(ps, *p)
		}
	}
	return ps, nil
}

// fakeProvider simule Mollie : status donne le statut renvoyé par GetPayment.
type fakeProvider struct {
	status  map[string]string
	amounts map[string]int64
	last    billing.PaymentRequest
	n       int
}

func newFakeProvider() *fakeProvider {
	return &fakeProvider{status: map[string]string{}, amounts: map[string]int64{}}
}

func (p *fakeProvider) Mode() string { return "test" }
func (p *fakeProvider) CreatePayment(ctx context.Context, req billing.PaymentRequest) (billing.PaymentInfo, error) {
	p.n++
	p.last = req
	id := "tr_" + string(rune('a'+p.n-1))
	p.status[id] = billing.StatusOpen
	p.amounts[id] = req.AmountCents
	return billing.PaymentInfo{ID: id, Status: billing.StatusOpen, AmountCents: req.AmountCents, Currency: req.Currency,
		CheckoutURL: "https://pay.example/" + id}, nil
}
func (p *fakeProvider) GetPayment(ctx context.Context, id string) (billing.PaymentInfo, error) {
	st, ok := p.status[id]
	if !ok {
		return billing.PaymentInfo{}, errors.New("unknown payment")
	}
	return billing.PaymentInfo{ID: id, Status: st, AmountCents: p.amounts[id], Currency: "EUR"}, nil
}
