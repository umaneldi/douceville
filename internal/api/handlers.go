// Package api expose les handlers HTTP et le routeur du backend Douceville.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/umaneldi/douceville/internal/auth"
	"github.com/umaneldi/douceville/internal/geo"
	"github.com/umaneldi/douceville/internal/mail"
	"github.com/umaneldi/douceville/internal/models"
	"github.com/umaneldi/douceville/internal/store"
	"golang.org/x/sync/errgroup"
)

// Isochroner calcule une isochrone (implémenté par geo.ORSClient).
type Isochroner interface {
	CalcIsochrone(ctx context.Context, lonlat [2]float64, dist, interval float64, transp string) (models.Isochrone, error)
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

// Handlers porte les dépendances des endpoints.
type Handlers struct {
	Store     store.Store
	Isochron  Isochroner
	Geocoder  Geocoder
	Suggester AddressSuggester
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

// maxOrigines limite le nombre de points de départ d'une recherche (chaque
// point coûte un appel OpenRouteService).
const maxOrigines = 10

// etablissementsInZone gère POST /etablissements. La zone de recherche est
// l'intersection des isochrones de tous les points de départ.
func (h *Handlers) etablissementsInZone(w http.ResponseWriter, r *http.Request) {
	log := logger(r.Context())

	var body models.QueryParameters
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		log.Warn("etablissements: invalid JSON body", "error", err)
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	origines := body.Origines
	if len(origines) == 0 {
		origines = []models.Origine{{Adresse: body.Adresse, Dist: body.Dist, Transp: body.Transp}}
	}
	if len(origines) > maxOrigines {
		log.Warn("etablissements: too many origins", "count", len(origines))
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("at most %d origins are allowed", maxOrigines))
		return
	}
	for i := range origines {
		origines[i].Adresse = strings.TrimSpace(origines[i].Adresse)
		if origines[i].Adresse == "" {
			log.Warn("etablissements: missing adresse", "origine", i)
			writeError(w, http.StatusUnprocessableEntity, "field 'adresse' is required")
			return
		}
	}

	// Géocodage et isochrone de chaque point de départ, en parallèle.
	isos := make([]models.Isochrone, len(origines))
	g, gctx := errgroup.WithContext(r.Context())
	for i, o := range origines {
		g.Go(func() error {
			ban, err := h.Geocoder.Geocode(gctx, o.Adresse)
			if err != nil {
				log.Error("geocoding failed", "origine", i, "adresse", o.Adresse, "error", err)
				return err
			}
			log.Debug("address geocoded", "origine", i, "adresse", o.Adresse, "lat", ban.Latitude, "lon", ban.Longitude)

			iso, err := h.Isochron.CalcIsochrone(gctx, [2]float64{ban.Longitude, ban.Latitude}, o.Dist, geo.LevelInterval(o.Dist), o.Transp)
			if err != nil {
				log.Error("isochrone computation failed", "origine", i, "lat", ban.Latitude, "lon", ban.Longitude,
					"dist", o.Dist, "transp", o.Transp, "error", err)
				return err
			}
			isos[i] = iso
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	resp := models.ListeEtablissementsAvecIsochrone{
		Etablissements:   []models.Etablissement{},
		Isochrone:        isos[0],
		Isochrones:       isos,
		Communes:         []models.CommuneSecurite{},
		Immobilier:       models.ImmobilierZone{Communes: []models.CommuneImmobilier{}, Quartiers: []models.QuartierImmobilier{}},
		LogementsSociaux: []models.LogementSocialPosition{},
	}

	// Zone de recherche : intersection des isochrones de temps maximal (dernier
	// niveau), calculée d'abord ; puis les niveaux emboîtés intérieurs.
	levels := zoneLevelWKTs(isos)
	if len(levels) == 0 {
		log.Info("etablissements searched in zone: no zone level", "count_origines", len(isos))
		writeJSON(w, http.StatusOK, resp)
		return
	}
	outer := levels[len(levels)-1]
	zoneWKT, zoneGJ, empty, err := h.Store.IntersectZones(r.Context(), outer.WKTs)
	if err != nil {
		log.Error("isochrones intersection failed", "error", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if empty {
		log.Info("etablissements searched in zone: empty intersection", "count_origines", len(isos))
		writeJSON(w, http.StatusOK, resp)
		return
	}
	resp.Zone = zoneGJ

	var levelWKT []string // WKT des niveaux non vides, par temps croissant
	resp.ZoneLevels = []models.ZoneLevel{}
	for _, lv := range levels[:len(levels)-1] {
		wkt, gj, empty, err := h.Store.IntersectZones(r.Context(), lv.WKTs)
		if err != nil {
			log.Error("zone level intersection failed", "dist", lv.Dist, "error", err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if empty {
			continue
		}
		levelWKT = append(levelWKT, wkt)
		resp.ZoneLevels = append(resp.ZoneLevels, models.ZoneLevel{Dist: lv.Dist, Geometry: gj})
	}
	levelWKT = append(levelWKT, zoneWKT)
	resp.ZoneLevels = append(resp.ZoneLevels, models.ZoneLevel{Dist: outer.Dist, Geometry: zoneGJ})

	etabs, err := h.Store.FindEtablissementsInZone(r.Context(), zoneWKT, body.Nature, body.Secteur)
	if err != nil {
		log.Error("zone search failed", "error", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if etabs != nil {
		resp.Etablissements = etabs
	}

	communes, err := h.Store.FindCommunesInZone(r.Context(), zoneWKT)
	if err != nil {
		log.Error("communes zone search failed", "error", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if communes != nil {
		resp.Communes = communes
	}

	codes := make([]string, len(resp.Communes))
	for i, c := range resp.Communes {
		codes[i] = c.CodeCommune
	}
	immo, err := h.Store.FindImmobilierInZone(r.Context(), zoneWKT, codes)
	if err != nil {
		log.Error("immobilier zone search failed", "error", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if immo.Communes == nil {
		immo.Communes = []models.CommuneImmobilier{}
	}
	if immo.Quartiers == nil {
		immo.Quartiers = []models.QuartierImmobilier{}
	}
	resp.Immobilier = immo

	logements, err := h.Store.FindLogementsSociauxInZone(r.Context(), zoneWKT)
	if err != nil {
		log.Error("logements sociaux zone search failed", "error", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if logements != nil {
		resp.LogementsSociaux = logements
	}

	if err := h.tagLevels(r.Context(), &resp, levelWKT); err != nil {
		log.Error("zone levels tagging failed", "error", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	log.Info("etablissements searched in zone",
		"count_origines", len(origines),
		"origines", origines,
		"nature", body.Nature,
		"secteur", body.Secteur,
		"count", len(resp.Etablissements),
		"count_communes", len(resp.Communes),
		"count_quartiers", len(resp.Immobilier.Quartiers),
		"count_logements_sociaux", len(resp.LogementsSociaux),
		"count_levels", len(resp.ZoneLevels),
	)

	writeJSON(w, http.StatusOK, resp)
}

// tagLevels renseigne le niveau (plus petit niveau de zone contenant le
// point) des établissements et des logements sociaux de resp ; levelWKT sont
// les WKT des niveaux de resp.ZoneLevels.
func (h *Handlers) tagLevels(ctx context.Context, resp *models.ListeEtablissementsAvecIsochrone, levelWKT []string) error {
	type target struct{ niveau **float64 }
	var points [][2]float64
	var targets []target
	for i := range resp.Etablissements {
		if p, ok := pointLonLat(resp.Etablissements[i].Position); ok {
			points = append(points, p)
			targets = append(targets, target{&resp.Etablissements[i].Niveau})
		}
	}
	for i := range resp.LogementsSociaux {
		l := &resp.LogementsSociaux[i]
		points = append(points, [2]float64{l.Lon, l.Lat})
		targets = append(targets, target{&l.Niveau})
	}
	if len(points) == 0 {
		return nil
	}

	ks, err := h.Store.PointLevels(ctx, points, levelWKT)
	if err != nil {
		return err
	}
	for i, k := range ks {
		if k >= 0 && k < len(resp.ZoneLevels) {
			d := resp.ZoneLevels[k].Dist
			*targets[i].niveau = &d
		}
	}
	return nil
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

	iso, err := h.Isochron.CalcIsochrone(r.Context(), [2]float64{lon, lat}, dist, 0, transp)
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
