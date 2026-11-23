package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/umaneldi/douceville/internal/billing"
	"github.com/umaneldi/douceville/internal/models"
	"github.com/umaneldi/douceville/internal/store"
)

const (
	// errCodeInsufficientCredit : solde insuffisant pour une requête payante (402).
	errCodeInsufficientCredit = "insufficient_credit"
	// maxTopupCents plafonne un rechargement (1 000 €).
	maxTopupCents = 100_000
	// maxPriceCents plafonne les tarifs et le crédit de bienvenue (100 €).
	maxPriceCents = 10_000
	// maxAdjustCents plafonne un ajustement manuel (10 000 €).
	maxAdjustCents = 1_000_000
	// transactionsLimit est le nombre de mouvements renvoyés.
	transactionsLimit = 50
	maxReasonLen      = 200
)

// billingEnabled indique si la facturation est active.
func (h *Handlers) billingEnabled() bool { return h.Billing != nil }

// charge débite le prix d'une requête payante (price calcule le prix à partir
// des tarifs). Rien n'est débité si la facturation est désactivée, si le
// compte est administrateur ou si le prix est nul. ok=false : la réponse
// d'erreur (402 ou 500) a été écrite. refund recrédite le montant débité ;
// il doit être appelé si la requête échoue ensuite.
func (h *Handlers) charge(w http.ResponseWriter, r *http.Request, desc string, price func(models.BillingSettings) int64) (refund func(), ok bool) {
	noop := func() {}
	if !h.billingEnabled() {
		return noop, true
	}
	user, ok := currentUser(w, r)
	if !ok {
		return noop, false
	}
	if user.Perms == models.PermsAdmin {
		return noop, true
	}
	log := logger(r.Context())

	settings, err := h.Store.GetBillingSettings(r.Context())
	if err != nil {
		log.Error("billing: could not read settings", "error", err)
		writeError(w, http.StatusInternalServerError, "could not read billing settings")
		return noop, false
	}
	cents := price(settings)
	if cents <= 0 {
		return noop, true
	}

	balance, err := h.Store.DebitCredit(r.Context(), user.ID, cents, desc)
	if errors.Is(err, store.ErrInsufficientCredit) {
		log.Info("billing: insufficient credit", "user_id", user.ID, "price_cents", cents, "balance_cents", user.BalanceCents)
		writeJSON(w, http.StatusPaymentRequired, map[string]any{
			"detail":        fmt.Sprintf("Crédit insuffisant : cette requête coûte %s.", formatPrice(cents, settings.Currency)),
			"code":          errCodeInsufficientCredit,
			"price_cents":   cents,
			"balance_cents": user.BalanceCents,
		})
		return noop, false
	}
	if err != nil {
		log.Error("billing: debit failed", "user_id", user.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not debit credit")
		return noop, false
	}
	w.Header().Set("X-Credit-Balance", strconv.FormatInt(balance, 10))
	log.Info("billing: request charged", "user_id", user.ID, "price_cents", cents, "balance_cents", balance)

	return func() {
		// La requête a pu être annulée : le remboursement doit aboutir quand même.
		ctx := context.WithoutCancel(r.Context())
		balance, err := h.Store.RefundCredit(ctx, user.ID, cents, "Remboursement : "+desc)
		if err != nil {
			log.Error("billing: refund failed", "user_id", user.ID, "amount_cents", cents, "error", err)
			return
		}
		w.Header().Set("X-Credit-Balance", strconv.FormatInt(balance, 10))
		log.Info("billing: request refunded", "user_id", user.ID, "amount_cents", cents, "balance_cents", balance)
	}, true
}

// formatPrice formate un montant en centimes pour un message ("0,05 €").
func formatPrice(cents int64, currency string) string {
	s := strings.Replace(billing.FormatAmount(cents), ".", ",", 1)
	if currency == "" || currency == "EUR" {
		return s + " €"
	}
	return s + " " + currency
}

// billingInfo est la réponse de GET /billing.
type billingInfo struct {
	Enabled bool `json:"enabled"`
	// Mode est "test" ou "live".
	Mode         string `json:"mode,omitempty"`
	BalanceCents int64  `json:"balance_cents"`
	// Free indique que le compte n'est pas facturé (administrateur).
	Free                bool                       `json:"free"`
	Currency            string                     `json:"currency,omitempty"`
	PricePerOriginCents int64                      `json:"price_per_origin_cents"`
	IsochronePriceCents int64                      `json:"isochrone_price_cents"`
	MinTopupCents       int64                      `json:"min_topup_cents"`
	MaxTopupCents       int64                      `json:"max_topup_cents"`
	Transactions        []models.CreditTransaction `json:"transactions"`
}

// getBilling gère GET /billing : solde, tarifs et derniers mouvements.
func (h *Handlers) getBilling(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}
	info := billingInfo{Enabled: h.billingEnabled(), BalanceCents: user.BalanceCents, Transactions: []models.CreditTransaction{}}
	if !info.Enabled {
		writeJSON(w, http.StatusOK, info)
		return
	}
	log := logger(r.Context())
	settings, err := h.Store.GetBillingSettings(r.Context())
	if err != nil {
		log.Error("billing: could not read settings", "error", err)
		writeError(w, http.StatusInternalServerError, "could not read billing settings")
		return
	}
	txs, err := h.Store.ListCreditTransactions(r.Context(), user.ID, transactionsLimit)
	if err != nil {
		log.Error("billing: could not list transactions", "user_id", user.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not list transactions")
		return
	}
	info.Mode = h.Billing.Mode()
	info.Free = user.Perms == models.PermsAdmin
	info.Currency = settings.Currency
	info.PricePerOriginCents = settings.PricePerOriginCents
	info.IsochronePriceCents = settings.IsochronePriceCents
	info.MinTopupCents = settings.MinTopupCents
	info.MaxTopupCents = maxTopupCents
	info.Transactions = txs
	writeJSON(w, http.StatusOK, info)
}

// topup gère POST /billing/topup : crée un paiement chez le prestataire et
// renvoie l'URL de la page de paiement.
func (h *Handlers) topup(w http.ResponseWriter, r *http.Request) {
	if !h.billingEnabled() {
		writeError(w, http.StatusNotFound, "billing is disabled")
		return
	}
	user, ok := currentUser(w, r)
	if !ok {
		return
	}
	log := logger(r.Context())

	var body models.TopupRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	settings, err := h.Store.GetBillingSettings(r.Context())
	if err != nil {
		log.Error("billing: could not read settings", "error", err)
		writeError(w, http.StatusInternalServerError, "could not read billing settings")
		return
	}
	if body.AmountCents < settings.MinTopupCents || body.AmountCents > maxTopupCents {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("le montant doit être compris entre %s et %s",
			formatPrice(settings.MinTopupCents, settings.Currency), formatPrice(maxTopupCents, settings.Currency)))
		return
	}

	p, err := h.Billing.CreatePayment(r.Context(), billing.PaymentRequest{
		AmountCents: body.AmountCents,
		Currency:    settings.Currency,
		Description: "Douceville — rechargement de crédit",
		RedirectURL: h.PublicURL + "/?billing_return=1",
		WebhookURL:  h.BillingWebhookURL,
		Metadata:    map[string]string{"user_id": user.ID},
	})
	if err != nil {
		log.Error("billing: could not create payment", "user_id", user.ID, "error", err)
		writeError(w, http.StatusBadGateway, "could not create the payment")
		return
	}
	if err := h.Store.CreatePayment(r.Context(), models.Payment{
		ID: p.ID, UserID: user.ID, AmountCents: body.AmountCents, Currency: settings.Currency,
		Status: p.Status, Mode: h.Billing.Mode(),
	}); err != nil {
		log.Error("billing: could not record payment", "user_id", user.ID, "payment_id", p.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not record the payment")
		return
	}
	log.Info("billing: payment created", "user_id", user.ID, "payment_id", p.ID, "amount_cents", body.AmountCents,
		"mode", h.Billing.Mode())
	writeJSON(w, http.StatusOK, map[string]string{"checkout_url": p.CheckoutURL, "payment_id": p.ID})
}

// applyPayment relit le paiement chez le prestataire et répercute son statut ;
// credited indique que le compte vient d'être crédité.
func (h *Handlers) applyPayment(ctx context.Context, stored models.Payment) (status string, credited bool, err error) {
	info, err := h.Billing.GetPayment(ctx, stored.ID)
	if err != nil {
		return "", false, err
	}
	if info.AmountCents != stored.AmountCents || !strings.EqualFold(info.Currency, stored.Currency) {
		return "", false, fmt.Errorf("payment %s: amount mismatch (%d %s, expected %d %s)",
			stored.ID, info.AmountCents, info.Currency, stored.AmountCents, stored.Currency)
	}
	if info.Status == billing.StatusPaid {
		credited, err = h.Store.MarkPaymentPaid(ctx, stored.ID)
		if err == nil && credited {
			logger(ctx).Info("billing: account credited", "user_id", stored.UserID, "payment_id", stored.ID,
				"amount_cents", stored.AmountCents)
		}
		return info.Status, credited, err
	}
	if info.Status != stored.Status {
		err = h.Store.UpdatePaymentStatus(ctx, stored.ID, info.Status)
	}
	return info.Status, false, err
}

// syncPayments gère POST /billing/sync : au retour de la page de paiement,
// relit les paiements en cours du compte (utile sans webhook, en local).
func (h *Handlers) syncPayments(w http.ResponseWriter, r *http.Request) {
	if !h.billingEnabled() {
		writeError(w, http.StatusNotFound, "billing is disabled")
		return
	}
	user, ok := currentUser(w, r)
	if !ok {
		return
	}
	log := logger(r.Context())

	pending, err := h.Store.ListPendingPayments(r.Context(), user.ID)
	if err != nil {
		log.Error("billing: could not list payments", "user_id", user.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not list payments")
		return
	}
	type result struct {
		ID          string `json:"id"`
		Status      string `json:"status"`
		AmountCents int64  `json:"amount_cents"`
		Credited    bool   `json:"credited"`
	}
	results := []result{}
	for _, p := range pending {
		status, credited, err := h.applyPayment(r.Context(), p)
		if err != nil {
			log.Error("billing: could not sync payment", "user_id", user.ID, "payment_id", p.ID, "error", err)
			continue
		}
		results = append(results, result{ID: p.ID, Status: status, AmountCents: p.AmountCents, Credited: credited})
	}
	writeJSON(w, http.StatusOK, map[string]any{"payments": results})
}

// mollieWebhook gère POST /billing/webhook/mollie. Mollie ne signe pas ses
// notifications : elles ne portent que l'identifiant du paiement, dont le
// statut est toujours relu auprès de l'API. Une erreur renvoie 500 pour que
// Mollie réessaie.
func (h *Handlers) mollieWebhook(w http.ResponseWriter, r *http.Request) {
	if !h.billingEnabled() {
		writeError(w, http.StatusNotFound, "billing is disabled")
		return
	}
	log := logger(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid form body")
		return
	}
	id := r.PostForm.Get("id")

	stored, err := h.Store.GetPayment(r.Context(), id)
	if err != nil {
		log.Error("billing: webhook: could not read payment", "payment_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not read payment")
		return
	}
	if stored == nil {
		// Paiement inconnu : on répond 200 sans rien révéler.
		log.Warn("billing: webhook for unknown payment", "payment_id", id)
		w.WriteHeader(http.StatusOK)
		return
	}
	status, _, err := h.applyPayment(r.Context(), *stored)
	if err != nil {
		log.Error("billing: webhook: could not apply payment", "payment_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not apply payment")
		return
	}
	log.Info("billing: webhook processed", "payment_id", id, "status", status)
	w.WriteHeader(http.StatusOK)
}

// getBillingSettings gère GET /admin/billing/settings.
func (h *Handlers) getBillingSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := h.Store.GetBillingSettings(r.Context())
	if err != nil {
		logger(r.Context()).Error("billing: could not read settings", "error", err)
		writeError(w, http.StatusInternalServerError, "could not read billing settings")
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

// updateBillingSettings gère PUT /admin/billing/settings.
func (h *Handlers) updateBillingSettings(w http.ResponseWriter, r *http.Request) {
	admin, ok := currentUser(w, r)
	if !ok {
		return
	}
	var body models.BillingSettings
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	for _, v := range []int64{body.PricePerOriginCents, body.IsochronePriceCents, body.WelcomeCreditCents} {
		if v < 0 || v > maxPriceCents {
			writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("prices must be between 0 and %d cents", maxPriceCents))
			return
		}
	}
	if body.MinTopupCents < 100 || body.MinTopupCents > maxTopupCents {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("min_topup_cents must be between 100 and %d", maxTopupCents))
		return
	}

	settings, err := h.Store.UpdateBillingSettings(r.Context(), body, admin.ID)
	if err != nil {
		logger(r.Context()).Error("billing: could not update settings", "admin_id", admin.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not update billing settings")
		return
	}
	logger(r.Context()).Info("billing: settings updated", "admin_id", admin.ID,
		"price_per_origin_cents", settings.PricePerOriginCents, "isochrone_price_cents", settings.IsochronePriceCents,
		"welcome_credit_cents", settings.WelcomeCreditCents, "min_topup_cents", settings.MinTopupCents)
	writeJSON(w, http.StatusOK, settings)
}

// adjustCredit gère POST /admin/users/{id}/credit : ajustement manuel du solde.
func (h *Handlers) adjustCredit(w http.ResponseWriter, r *http.Request) {
	admin, ok := currentUser(w, r)
	if !ok {
		return
	}
	log := logger(r.Context())
	id := chi.URLParam(r, "id")

	var body models.CreditAdjustment
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	body.Reason = strings.TrimSpace(body.Reason)
	if body.DeltaCents == 0 || body.DeltaCents > maxAdjustCents || body.DeltaCents < -maxAdjustCents {
		writeError(w, http.StatusUnprocessableEntity, "delta_cents must be non-zero and reasonable")
		return
	}
	if body.Reason == "" || utf8.RuneCountInString(body.Reason) > maxReasonLen {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("a reason (at most %d characters) is required", maxReasonLen))
		return
	}

	balance, err := h.Store.AdjustCredit(r.Context(), id, body.DeltaCents, admin.ID, body.Reason)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "user not found")
		return
	case errors.Is(err, store.ErrInsufficientCredit):
		writeError(w, http.StatusUnprocessableEntity, "the balance cannot become negative")
		return
	case err != nil:
		log.Error("billing: could not adjust credit", "admin_id", admin.ID, "user_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not adjust the credit")
		return
	}
	log.Info("billing: credit adjusted", "admin_id", admin.ID, "user_id", id, "delta_cents", body.DeltaCents,
		"balance_cents", balance, "reason", body.Reason)
	writeJSON(w, http.StatusOK, map[string]int64{"balance_cents": balance})
}

// listUserTransactions gère GET /admin/users/{id}/transactions.
func (h *Handlers) listUserTransactions(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	txs, err := h.Store.ListCreditTransactions(r.Context(), id, transactionsLimit)
	if err != nil {
		logger(r.Context()).Error("billing: could not list transactions", "user_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not list transactions")
		return
	}
	writeJSON(w, http.StatusOK, txs)
}
