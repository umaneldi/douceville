// Package billing regroupe les clients des prestataires de paiement utilisés
// pour recharger le crédit prépayé des comptes.
package billing

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Statuts de paiement (vocabulaire Mollie, repris pour tout prestataire).
const (
	StatusOpen       = "open"
	StatusPending    = "pending"
	StatusAuthorized = "authorized"
	StatusPaid       = "paid"
	StatusCanceled   = "canceled"
	StatusExpired    = "expired"
	StatusFailed     = "failed"
)

// PaymentRequest décrit un paiement à créer.
type PaymentRequest struct {
	AmountCents int64
	Currency    string
	Description string
	// RedirectURL est la page où revient le client après paiement.
	RedirectURL string
	// WebhookURL est notifiée des changements de statut (vide : aucune).
	WebhookURL string
	Metadata   map[string]string
}

// PaymentInfo est l'état d'un paiement chez le prestataire.
type PaymentInfo struct {
	ID          string
	Status      string
	AmountCents int64
	Currency    string
	// CheckoutURL est la page de paiement (renseignée à la création).
	CheckoutURL string
	Metadata    map[string]string
}

// PaymentProvider est un prestataire de paiement (Mollie ; Stripe pourrait
// l'implémenter à son tour).
type PaymentProvider interface {
	CreatePayment(ctx context.Context, req PaymentRequest) (PaymentInfo, error)
	GetPayment(ctx context.Context, id string) (PaymentInfo, error)
	// Mode est "test" ou "live".
	Mode() string
}

// FormatAmount formate des centimes au format décimal "12.34".
func FormatAmount(cents int64) string {
	sign := ""
	if cents < 0 {
		sign, cents = "-", -cents
	}
	return fmt.Sprintf("%s%d.%02d", sign, cents/100, cents%100)
}

// ParseAmount convertit un montant décimal ("12.34", "5", "5.5") en centimes.
func ParseAmount(s string) (int64, error) {
	whole, frac, _ := strings.Cut(strings.TrimSpace(s), ".")
	if len(frac) > 2 {
		return 0, fmt.Errorf("invalid amount %q", s)
	}
	for len(frac) < 2 {
		frac += "0"
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || w < 0 {
		return 0, fmt.Errorf("invalid amount %q", s)
	}
	f, err := strconv.ParseInt(frac, 10, 64)
	if err != nil || f < 0 {
		return 0, fmt.Errorf("invalid amount %q", s)
	}
	return w*100 + f, nil
}
