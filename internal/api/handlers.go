// Package api expose les handlers HTTP et le routeur du backend Douceville.
package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/umaneldi/douceville/internal/auth"
	"github.com/umaneldi/douceville/internal/geo"
	"github.com/umaneldi/douceville/internal/mail"
	"github.com/umaneldi/douceville/internal/models"
	"github.com/umaneldi/douceville/internal/store"
)

// Isochroner calcule une isochrone (implémenté par geo.ORSClient).
type Isochroner interface {
	CalcIsochrone(ctx context.Context, lonlat [2]float64, dist float64, transp string) (models.Isochrone, error)
}

// Geocoder résout une adresse en coordonnées (implémenté par geo.BANClient).
type Geocoder interface {
	Geocode(ctx context.Context, query string) (geo.BANResult, error)
}

// Handlers porte les dépendances des endpoints.
type Handlers struct {
	Store     store.Store
	Isochron  Isochroner
	Geocoder  Geocoder
	JWTSecret string
	// Mailer envoie les e-mails de vérification.
	Mailer mail.Sender
	// PublicURL est la base des liens envoyés par e-mail (sans "/" final).
	PublicURL string
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

// etablissementsInZone gère POST /etablissements.
func (h *Handlers) etablissementsInZone(w http.ResponseWriter, r *http.Request) {
	log := logger(r.Context())

	var body models.QueryParameters
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		log.Warn("etablissements: invalid JSON body", "error", err)
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Adresse == "" {
		log.Warn("etablissements: missing adresse")
		writeError(w, http.StatusUnprocessableEntity, "field 'adresse' is required")
		return
	}

	ban, err := h.Geocoder.Geocode(r.Context(), body.Adresse)
	if err != nil {
		log.Error("geocoding failed", "adresse", body.Adresse, "error", err)
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	log.Debug("address geocoded", "adresse", body.Adresse, "lat", ban.Latitude, "lon", ban.Longitude)

	iso, err := h.Isochron.CalcIsochrone(r.Context(), [2]float64{ban.Longitude, ban.Latitude}, body.Dist, body.Transp)
	if err != nil {
		log.Error("isochrone computation failed", "lat", ban.Latitude, "lon", ban.Longitude, "dist", body.Dist, "transp", body.Transp, "error", err)
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	etabs, err := h.Store.FindEtablissementsInZone(r.Context(), geo.PolygonWKT(iso), body.Nature, body.Secteur)
	if err != nil {
		log.Error("zone search failed", "error", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	log.Info("etablissements searched in zone",
		"adresse", body.Adresse,
		"dist", body.Dist,
		"transp", body.Transp,
		"nature", body.Nature,
		"secteur", body.Secteur,
		"count", len(etabs),
	)

	writeJSON(w, http.StatusOK, models.ListeEtablissementsAvecIsochrone{
		Etablissements: etabs,
		Isochrone:      iso,
	})
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

	iso, err := h.Isochron.CalcIsochrone(r.Context(), [2]float64{lon, lat}, dist, transp)
	if err != nil {
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
