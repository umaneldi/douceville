// Package api expose les handlers HTTP et le routeur du backend Douceville.
package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/umaneldi/douceville/internal/auth"
	"github.com/umaneldi/douceville/internal/billing"
	"github.com/umaneldi/douceville/internal/geo"
	"github.com/umaneldi/douceville/internal/mail"
	"github.com/umaneldi/douceville/internal/models"
	"github.com/umaneldi/douceville/internal/store"
)

// Isochroner calcule des isochrones (implémenté par geo.ORSClient et
// geo.ValhallaClient).
type Isochroner interface {
	// CalcIsochrone calcule l'isochrone d'une position (GET /isochrone).
	CalcIsochrone(ctx context.Context, lonlat [2]float64, dist, interval float64, transp string) (models.Isochrone, error)
	// CalcIsochrones calcule l'isochrone de temps dist de chaque position,
	// dans l'ordre (recherche par établissements).
	CalcIsochrones(ctx context.Context, lonlats [][2]float64, dist float64, transp string) ([]models.Isochrone, error)
}

// RoutePlanner calcule des itinéraires routiers (implémenté par
// geo.ORSClient et geo.ValhallaClient).
type RoutePlanner interface {
	// CalcItineraire calcule l'itinéraire de from à to ([lon, lat]) pour un
	// profil de transport (models.Transports).
	CalcItineraire(ctx context.Context, from, to [2]float64, transp string) (models.Itineraire, error)
}

// Geocoder résout une adresse en coordonnées (implémenté par geo.BANClient).
type Geocoder interface {
	Geocode(ctx context.Context, query string) (geo.BANResult, error)
}

// AddressSuggester propose des adresses à partir d'une saisie partielle
// (implémenté par geo.BANClient).
type AddressSuggester interface {
	Suggest(ctx context.Context, query string, limit int) ([]geo.BANSuggestion, error)
}

// OIDCProvider gère le flux de connexion OpenID Connect (implémenté par auth.OIDC).
type OIDCProvider interface {
	// DisplayName est le libellé du bouton de connexion.
	DisplayName() string
	// NewUserPerms est le niveau attribué aux comptes créés via OIDC.
	NewUserPerms() models.Perms
	AuthURL(w http.ResponseWriter) (string, error)
	Exchange(w http.ResponseWriter, r *http.Request) (models.OIDCIdentity, error)
}

// Handlers porte les dépendances des endpoints.
type Handlers struct {
	Store    store.Store
	Isochron Isochroner
	// Routes calcule les itinéraires de la fiche d'un quartier.
	Routes    RoutePlanner
	Geocoder  Geocoder
	Suggester AddressSuggester
	JWTSecret string
	// Mailer envoie les e-mails de vérification.
	Mailer mail.Sender
	// PublicURL est la base des liens envoyés par e-mail (sans "/" final).
	PublicURL string
	// OIDC est le client OpenID Connect ; nil si la connexion OIDC est désactivée.
	OIDC OIDCProvider
	// Billing est le prestataire de paiement ; nil si la facturation est désactivée.
	Billing billing.PaymentProvider
	// BillingWebhookURL est l'URL de notification transmise au prestataire.
	BillingWebhookURL string
	// IsoCacheTTL est la durée de validité d'une isochrone en cache (30 jours
	// si nulle) ; IsoCacheMax le nombre maximal d'isochrones en cache (sans
	// limite si nul).
	IsoCacheTTL time.Duration
	IsoCacheMax int
	// SourcesParNature plafonne le nombre d'établissements sources retenus par
	// critère d'une recherche (chacun coûte une isochrone) ; 0 : pas de plafond.
	SourcesParNature int
	// SearchCostWarningCents est le coût d'une recherche au-delà duquel
	// l'interface demande confirmation (0 : jamais), transmis par GET /billing
	// et POST /etablissements/estimation.
	SearchCostWarningCents int64
}

// readEtablissement gère GET /etablissement/{uai}.
func (h *Handlers) readEtablissement(w http.ResponseWriter, r *http.Request) {
	uai := chi.URLParam(r, "uai")
	log := logger(r.Context()).With("uai", uai)

	etab, err := h.Store.GetEtablissementByUAI(r.Context(), uai)
	if err != nil {
		log.Error("etablissement lookup failed", "error", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if etab == nil {
		log.Warn("etablissement not found")
		writeError(w, http.StatusNotFound, "Etablissement '"+uai+"' not found")
		return
	}

	log.Info("etablissement read")
	writeJSON(w, http.StatusOK, etab)
}

// isochrone gère GET /isochrone.
func (h *Handlers) isochrone(w http.ResponseWriter, r *http.Request) {
	log := logger(r.Context())

	q := r.URL.Query()
	lat, err1 := parseFloat(q.Get("lat"))
	lon, err2 := parseFloat(q.Get("lon"))
	dist, err3 := parseFloat(q.Get("dist"))
	if err1 != nil || err2 != nil || err3 != nil {
		log.Warn("isochrone: invalid query params", "query", r.URL.RawQuery)
		writeError(w, http.StatusUnprocessableEntity, "query params 'lat', 'lon' and 'dist' must be valid floats")
		return
	}
	transp := q.Get("transp")

	refund, ok := h.charge(w, r, "Isochrone", func(s models.BillingSettings) int64 { return s.IsochronePriceCents })
	if !ok {
		return
	}
	iso, err := h.Isochron.CalcIsochrone(r.Context(), [2]float64{lon, lat}, dist, 0, transp)
	if err != nil {
		refund()
		log.Error("isochrone computation failed", "lat", lat, "lon", lon, "dist", dist, "transp", transp, "error", err)
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	log.Info("isochrone computed", "lat", lat, "lon", lon, "dist", dist, "transp", transp)
	writeJSON(w, http.StatusOK, iso)
}

// getUser gère GET /user.
func (h *Handlers) getUser(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		logger(r.Context()).Warn("user: no authenticated user in context")
		writeError(w, http.StatusUnauthorized, "Not authenticated")
		return
	}
	writeJSON(w, http.StatusOK, user)
}

// addressSuggest gère GET /address/suggest?q=… (autocomplétion d'adresse).
func (h *Handlers) addressSuggest(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(q)) < 3 || h.Suggester == nil {
		writeJSON(w, http.StatusOK, []geo.BANSuggestion{})
		return
	}

	suggestions, err := h.Suggester.Suggest(r.Context(), q, 5)
	if err != nil {
		logger(r.Context()).Error("address suggest failed", "q", q, "error", err)
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if suggestions == nil {
		suggestions = []geo.BANSuggestion{}
	}
	writeJSON(w, http.StatusOK, suggestions)
}
