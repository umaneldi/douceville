package models

import "time"

// Types de mouvements de crédit (colonne credit_transaction.kind).
const (
	CreditTopup       = "topup"
	CreditUsage       = "usage"
	CreditRefund      = "refund"
	CreditAdminAdjust = "admin_adjust"
	CreditWelcome     = "welcome"
)

// BillingSettings sont les tarifs et paramètres de facturation, modifiables
// par un administrateur. Les montants sont en centimes.
type BillingSettings struct {
	PricePerOriginCents int64      `json:"price_per_origin_cents"`
	IsochronePriceCents int64      `json:"isochrone_price_cents"`
	WelcomeCreditCents  int64      `json:"welcome_credit_cents"`
	MinTopupCents       int64      `json:"min_topup_cents"`
	Currency            string     `json:"currency"`
	UpdatedAt           *time.Time `json:"updated_at,omitempty"`
}

// CreditTransaction est un mouvement du solde d'un compte (montant signé, en centimes).
type CreditTransaction struct {
	ID          int64     `json:"id"`
	AmountCents int64     `json:"amount_cents"`
	Kind        string    `json:"kind"`
	Description string    `json:"description"`
	Reference   *string   `json:"reference"`
	CreatedAt   time.Time `json:"created_at"`
}

// Payment est un paiement de rechargement auprès du prestataire.
type Payment struct {
	ID          string `json:"id"`
	UserID      string `json:"user_id"`
	AmountCents int64  `json:"amount_cents"`
	Currency    string `json:"currency"`
	Status      string `json:"status"`
	Mode        string `json:"mode"`
}

// TopupRequest est le corps de POST /billing/topup.
type TopupRequest struct {
	AmountCents int64 `json:"amount_cents"`
}

// CreditAdjustment est le corps de POST /admin/users/{id}/credit.
type CreditAdjustment struct {
	DeltaCents int64  `json:"delta_cents"`
	Reason     string `json:"reason"`
}
