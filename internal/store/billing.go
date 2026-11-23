package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/umaneldi/douceville/internal/models"
)

// ErrInsufficientCredit est renvoyée quand le solde ne couvre pas un débit.
var ErrInsufficientCredit = errors.New("insufficient credit")

// ErrNotFound est renvoyée quand le compte ou le paiement visé n'existe pas.
var ErrNotFound = errors.New("not found")

// grantWelcomeCredit crédite un compte tout juste créé du crédit de bienvenue
// configuré et renvoie son solde.
func grantWelcomeCredit(ctx context.Context, tx pgx.Tx, userID string) (int64, error) {
	var amount int64
	err := tx.QueryRow(ctx, `SELECT welcome_credit_cents FROM billing_settings WHERE id`).Scan(&amount)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && amount <= 0) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return applyCredit(ctx, tx, userID, amount, models.CreditWelcome, "Crédit de bienvenue", nil, nil)
}

// applyCredit ajoute delta (signé) au solde du compte et journalise le
// mouvement. Renvoie ErrInsufficientCredit si le solde deviendrait négatif,
// ErrNotFound si le compte n'existe pas.
func applyCredit(ctx context.Context, tx pgx.Tx, userID string, delta int64, kind, desc string, ref, by *string) (int64, error) {
	var balance int64
	err := tx.QueryRow(ctx, `UPDATE utilisateur SET balance_cents = balance_cents + $2
		WHERE id = $1 AND balance_cents + $2 >= 0 RETURNING balance_cents`, userID, delta).Scan(&balance)
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM utilisateur WHERE id = $1)`, userID).Scan(&exists); err != nil {
			return 0, err
		}
		if !exists {
			return 0, ErrNotFound
		}
		return 0, ErrInsufficientCredit
	}
	if err != nil {
		return 0, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO credit_transaction (user_id, amount_cents, kind, description, reference, created_by)
		VALUES ($1, $2, $3, $4, $5, $6)`, userID, delta, kind, desc, ref, by)
	return balance, err
}

// inTx exécute fn dans une transaction validée si fn réussit.
func (s *PgStore) inTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // sans effet après Commit
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// GetBillingSettings implémente Store.GetBillingSettings.
func (s *PgStore) GetBillingSettings(ctx context.Context) (models.BillingSettings, error) {
	var b models.BillingSettings
	err := s.pool.QueryRow(ctx, `SELECT price_per_origin_cents, isochrone_price_cents, welcome_credit_cents,
		min_topup_cents, currency, updated_at FROM billing_settings WHERE id`).Scan(
		&b.PricePerOriginCents, &b.IsochronePriceCents, &b.WelcomeCreditCents, &b.MinTopupCents, &b.Currency, &b.UpdatedAt)
	return b, err
}

// UpdateBillingSettings implémente Store.UpdateBillingSettings (la devise
// n'est pas modifiable).
func (s *PgStore) UpdateBillingSettings(ctx context.Context, b models.BillingSettings, adminID string) (models.BillingSettings, error) {
	var out models.BillingSettings
	err := s.pool.QueryRow(ctx, `INSERT INTO billing_settings AS b (id, price_per_origin_cents, isochrone_price_cents,
			welcome_credit_cents, min_topup_cents, updated_at, updated_by)
		VALUES (TRUE, $1, $2, $3, $4, now(), $5)
		ON CONFLICT (id) DO UPDATE SET price_per_origin_cents = EXCLUDED.price_per_origin_cents,
			isochrone_price_cents = EXCLUDED.isochrone_price_cents, welcome_credit_cents = EXCLUDED.welcome_credit_cents,
			min_topup_cents = EXCLUDED.min_topup_cents, updated_at = now(), updated_by = EXCLUDED.updated_by
		RETURNING price_per_origin_cents, isochrone_price_cents, welcome_credit_cents, min_topup_cents, currency, updated_at`,
		b.PricePerOriginCents, b.IsochronePriceCents, b.WelcomeCreditCents, b.MinTopupCents, adminID).Scan(
		&out.PricePerOriginCents, &out.IsochronePriceCents, &out.WelcomeCreditCents, &out.MinTopupCents, &out.Currency, &out.UpdatedAt)
	return out, err
}

// DebitCredit implémente Store.DebitCredit.
func (s *PgStore) DebitCredit(ctx context.Context, userID string, cents int64, desc string) (int64, error) {
	var balance int64
	err := s.inTx(ctx, func(tx pgx.Tx) (err error) {
		balance, err = applyCredit(ctx, tx, userID, -cents, models.CreditUsage, desc, nil, nil)
		return err
	})
	return balance, err
}

// RefundCredit implémente Store.RefundCredit.
func (s *PgStore) RefundCredit(ctx context.Context, userID string, cents int64, desc string) (int64, error) {
	var balance int64
	err := s.inTx(ctx, func(tx pgx.Tx) (err error) {
		balance, err = applyCredit(ctx, tx, userID, cents, models.CreditRefund, desc, nil, nil)
		return err
	})
	return balance, err
}

// AdjustCredit implémente Store.AdjustCredit.
func (s *PgStore) AdjustCredit(ctx context.Context, userID string, delta int64, adminID, reason string) (int64, error) {
	var balance int64
	err := s.inTx(ctx, func(tx pgx.Tx) (err error) {
		balance, err = applyCredit(ctx, tx, userID, delta, models.CreditAdminAdjust, reason, nil, &adminID)
		return err
	})
	return balance, err
}

// ListCreditTransactions implémente Store.ListCreditTransactions.
func (s *PgStore) ListCreditTransactions(ctx context.Context, userID string, limit int) ([]models.CreditTransaction, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, amount_cents, kind, description, reference, created_at
		FROM credit_transaction WHERE user_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	txs := []models.CreditTransaction{}
	for rows.Next() {
		var t models.CreditTransaction
		if err := rows.Scan(&t.ID, &t.AmountCents, &t.Kind, &t.Description, &t.Reference, &t.CreatedAt); err != nil {
			return nil, err
		}
		txs = append(txs, t)
	}
	return txs, rows.Err()
}

// CreatePayment implémente Store.CreatePayment.
func (s *PgStore) CreatePayment(ctx context.Context, p models.Payment) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO payment (id, user_id, amount_cents, currency, status, mode)
		VALUES ($1, $2, $3, $4, $5, $6)`, p.ID, p.UserID, p.AmountCents, p.Currency, p.Status, p.Mode)
	return err
}

// GetPayment implémente Store.GetPayment.
func (s *PgStore) GetPayment(ctx context.Context, id string) (*models.Payment, error) {
	var p models.Payment
	err := s.pool.QueryRow(ctx, `SELECT id, user_id, amount_cents, currency, status, mode FROM payment WHERE id = $1`, id).Scan(
		&p.ID, &p.UserID, &p.AmountCents, &p.Currency, &p.Status, &p.Mode)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// MarkPaymentPaid implémente Store.MarkPaymentPaid. Le passage à 'paid' et le
// crédit du compte ont lieu dans la même transaction, une seule fois.
func (s *PgStore) MarkPaymentPaid(ctx context.Context, id string) (bool, error) {
	credited := false
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var userID string
		var amount int64
		err := tx.QueryRow(ctx, `UPDATE payment SET status = 'paid', paid_at = now()
			WHERE id = $1 AND status <> 'paid' RETURNING user_id, amount_cents`, id).Scan(&userID, &amount)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // inconnu ou déjà crédité
		}
		if err != nil {
			return err
		}
		if _, err := applyCredit(ctx, tx, userID, amount, models.CreditTopup, "Rechargement", &id, nil); err != nil {
			return err
		}
		credited = true
		return nil
	})
	return credited, err
}

// UpdatePaymentStatus implémente Store.UpdatePaymentStatus (jamais pour un
// paiement déjà crédité).
func (s *PgStore) UpdatePaymentStatus(ctx context.Context, id, status string) error {
	_, err := s.pool.Exec(ctx, `UPDATE payment SET status = $2 WHERE id = $1 AND status <> 'paid'`, id, status)
	return err
}

// ListPendingPayments implémente Store.ListPendingPayments.
func (s *PgStore) ListPendingPayments(ctx context.Context, userID string) ([]models.Payment, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, user_id, amount_cents, currency, status, mode FROM payment
		WHERE user_id = $1 AND status IN ('open', 'pending', 'authorized') AND created_at > now() - interval '7 days'
		ORDER BY created_at DESC LIMIT 20`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ps := []models.Payment{}
	for rows.Next() {
		var p models.Payment
		if err := rows.Scan(&p.ID, &p.UserID, &p.AmountCents, &p.Currency, &p.Status, &p.Mode); err != nil {
			return nil, err
		}
		ps = append(ps, p)
	}
	return ps, rows.Err()
}
