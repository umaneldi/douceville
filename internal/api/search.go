package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/umaneldi/douceville/internal/geo"
	"github.com/umaneldi/douceville/internal/models"
	"golang.org/x/sync/errgroup"
)

const (
	// rayonMaxKm borne le rayon de recherche des établissements sources.
	rayonMaxKm = 50
	// distMin et distMax bornent le temps de trajet d'un critère (secondes).
	distMin, distMax = 60, 3600
	// defaultIsoCacheTTL est la validité d'une isochrone en cache si
	// Handlers.IsoCacheTTL n'est pas renseigné.
	defaultIsoCacheTTL = 30 * 24 * time.Hour
)

// validateCriteres vérifie et normalise les critères : nature connue et sans
// doublon, secteurs connus, filières connues (critère Lycée seulement), plage de taux dans [0, 100] (taux_max absent ou
// nul vaut 100) et,
// si withIso, temps de trajet dans [distMin, distMax] et mode de transport
// connu (driving-car par défaut). Le message d'erreur est destiné au client.
func validateCriteres(criteres []models.CritereNature, withIso bool) error {
	seen := map[string]bool{}
	for i := range criteres {
		c := &criteres[i]
		if !slices.Contains(models.Natures, c.Nature) {
			return fmt.Errorf("criteres[%d]: unknown nature %q", i, c.Nature)
		}
		if seen[c.Nature] {
			return fmt.Errorf("criteres[%d]: duplicate nature %q", i, c.Nature)
		}
		seen[c.Nature] = true
		for _, s := range c.Secteur {
			if !slices.Contains(models.Secteurs, s) {
				return fmt.Errorf("criteres[%d]: unknown secteur %q", i, s)
			}
		}
		if len(c.Filiere) > 0 && c.Nature != models.NatureLycee {
			return fmt.Errorf("criteres[%d]: filiere is only allowed for nature %q", i, models.NatureLycee)
		}
		for _, f := range c.Filiere {
			if !slices.Contains(models.Filieres, f) {
				return fmt.Errorf("criteres[%d]: unknown filiere %q", i, f)
			}
		}
		if c.TauxMax == 0 { // absent : pas de borne haute
			c.TauxMax = 100
		}
		if c.TauxMin < 0 || c.TauxMax > 100 || c.TauxMin > c.TauxMax {
			return fmt.Errorf("criteres[%d]: taux range must satisfy 0 <= taux_min <= taux_max <= 100", i)
		}
		if !withIso {
			continue
		}
		if c.Dist < distMin || c.Dist > distMax {
			return fmt.Errorf("criteres[%d]: dist must be between %d and %d seconds", i, distMin, distMax)
		}
		if c.Transp == "" {
			c.Transp = "driving-car"
		}
		if !slices.Contains(models.Transports, c.Transp) {
			return fmt.Errorf("criteres[%d]: unknown transp %q", i, c.Transp)
		}
	}
	return nil
}

// validateFiltres vérifie les filtres de zone.
func validateFiltres(f models.FiltresZone) error {
	neg := func(p *float64) bool { return p != nil && *p < 0 }
	if neg(f.PrixM2Min) || neg(f.PrixM2Max) || neg(f.FiscaliteMax) {
		return errors.New("zone filters must be positive")
	}
	if f.PrixM2Min != nil && f.PrixM2Max != nil && *f.PrixM2Min > *f.PrixM2Max {
		return errors.New("prix_m2_min must not exceed prix_m2_max")
	}
	if f.SecuriteMin != nil && (*f.SecuriteMin < 0 || *f.SecuriteMin > 100) {
		return errors.New("securite_min must be between 0 and 100")
	}
	return nil
}

// isoCacheTTL renvoie la durée de validité des isochrones en cache.
func (h *Handlers) isoCacheTTL() time.Duration {
	if h.IsoCacheTTL > 0 {
		return h.IsoCacheTTL
	}
	return defaultIsoCacheTTL
}

// isoGroup regroupe les isochrones manquantes de même temps et même mode
// (un appel au fournisseur de routage par groupe).
type isoGroup struct {
	dist   float64
	transp string
	keys   []models.IsochroneKey
	pts    [][2]float64
}

// searchPlan est la préparation d'une recherche, commune à la recherche et à
// son estimation : corps validé, centre géocodé, sources, et isochrones en
// cache ou à calculer.
type searchPlan struct {
	body   models.SearchRequest
	centre [2]float64
	// sources sont les établissements sources ; criteresVides les natures
	// sans source (la zone est alors vide : rien d'autre n'est préparé).
	sources       []models.Source
	criteresVides []string
	// keys[i] est la clé de cache de sources[i] ; unique les clés distinctes.
	keys   []models.IsochroneKey
	unique []models.IsochroneKey
	// rings sont les isochrones trouvées en cache ; groups les manquantes,
	// par temps et mode ; misses leur nombre.
	rings  map[models.IsochroneKey][][2]float64
	groups []*isoGroup
	misses int
}

// prepareSearch décode et valide le corps d'une recherche, géocode le centre,
// choisit les sources (au plus h.SourcesParNature par critère, sans plafond
// si nul) et lit les isochrones en cache. op préfixe les journaux. En cas
// d'erreur, la réponse est écrite et ok vaut false.
func (h *Handlers) prepareSearch(w http.ResponseWriter, r *http.Request, op string) (plan *searchPlan, ok bool) {
	log := logger(r.Context())
	ctx := r.Context()

	var body models.SearchRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		log.Warn(op+": invalid JSON body", "error", err)
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return nil, false
	}
	body.Centre = strings.TrimSpace(body.Centre)
	var verr error
	switch {
	case body.Centre == "":
		verr = errors.New("field 'centre' is required")
	case body.RayonKm <= 0 || body.RayonKm > rayonMaxKm:
		verr = fmt.Errorf("rayon_km must be in ]0, %d]", rayonMaxKm)
	case len(body.Criteres) == 0 || len(body.Criteres) > len(models.Natures):
		verr = fmt.Errorf("between 1 and %d criteres are required", len(models.Natures))
	default:
		if verr = validateCriteres(body.Criteres, true); verr == nil {
			verr = validateFiltres(body.FiltresZone)
		}
	}
	if verr != nil {
		log.Warn(op+": invalid parameters", "error", verr)
		writeError(w, http.StatusUnprocessableEntity, verr.Error())
		return nil, false
	}

	ban, err := h.Geocoder.Geocode(ctx, body.Centre)
	if err != nil {
		log.Error(op+": geocoding failed", "centre", body.Centre, "error", err)
		writeError(w, http.StatusBadGateway, err.Error())
		return nil, false
	}
	plan = &searchPlan{body: body, centre: [2]float64{ban.Longitude, ban.Latitude}, sources: []models.Source{}, criteresVides: []string{}}

	sources, err := h.Store.SelectSourceEtablissements(ctx, plan.centre, body.RayonKm*1000, body.Criteres, h.SourcesParNature)
	if err != nil {
		log.Error(op+": source selection failed", "error", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return nil, false
	}
	if sources != nil {
		plan.sources = sources
	}
	for _, c := range body.Criteres {
		if !slices.ContainsFunc(plan.sources, func(s models.Source) bool { return s.Critere == c.Nature }) {
			plan.criteresVides = append(plan.criteresVides, c.Nature)
		}
	}
	if len(plan.criteresVides) > 0 {
		return plan, true
	}

	// Clé de cache de chaque source.
	plan.keys = make([]models.IsochroneKey, len(plan.sources))
	for i, s := range plan.sources {
		ll, ok := s.LonLat()
		if !ok {
			log.Error(op+": source without valid position", "uai", s.UAI)
			writeError(w, http.StatusInternalServerError, "source "+s.UAI+" has no valid position")
			return nil, false
		}
		plan.keys[i] = models.NewIsochroneKey(ll, s.Dist, s.Transp)
	}
	plan.unique = slices.Compact(slices.SortedFunc(slices.Values(plan.keys), compareKeys))

	// Le cache n'est qu'une optimisation : une erreur de lecture est ignorée.
	plan.rings, err = h.Store.GetCachedIsochrones(ctx, plan.unique, h.isoCacheTTL())
	if err != nil {
		log.Warn(op+": isochrone cache read failed", "error", err)
		plan.rings = nil
	}
	if plan.rings == nil {
		plan.rings = map[models.IsochroneKey][][2]float64{}
	}
	for _, k := range plan.unique {
		if _, ok := plan.rings[k]; ok {
			continue
		}
		plan.misses++
		var g *isoGroup
		for _, gg := range plan.groups {
			if gg.dist == float64(k.Dist) && gg.transp == k.Transp {
				g = gg
			}
		}
		if g == nil {
			g = &isoGroup{dist: float64(k.Dist), transp: k.Transp}
			plan.groups = append(plan.groups, g)
		}
		g.keys = append(g.keys, k)
		g.pts = append(g.pts, [2]float64{float64(k.LonE5) / 1e5, float64(k.LatE5) / 1e5})
	}
	return plan, true
}

// etablissementsInZone gère POST /etablissements. Les établissements sources
// sont choisis autour du centre selon un critère par nature ; la zone est
// l'intersection, entre critères, de l'union des isochrones des sources de
// chaque critère (près d'au moins une source de chaque nature), restreinte
// par les filtres.
func (h *Handlers) etablissementsInZone(w http.ResponseWriter, r *http.Request) {
	log := logger(r.Context())
	ctx := r.Context()

	plan, ok := h.prepareSearch(w, r, "search")
	if !ok {
		return
	}
	body := plan.body
	resp := models.ListeEtablissementsAvecIsochrone{
		Centre:           models.Centre{Lon: plan.centre[0], Lat: plan.centre[1], RayonKm: body.RayonKm},
		Sources:          plan.sources,
		CriteresVides:    plan.criteresVides,
		Etablissements:   []models.Etablissement{},
		Communes:         []models.CommuneSecurite{},
		Immobilier:       models.ImmobilierZone{Communes: []models.CommuneImmobilier{}, Quartiers: []models.QuartierImmobilier{}},
		LogementsSociaux: []models.LogementSocialPosition{},
	}
	if len(resp.CriteresVides) > 0 {
		log.Info("search: no source for some criteres", "criteres_vides", resp.CriteresVides, "count_sources", len(resp.Sources))
		writeJSON(w, http.StatusOK, resp)
		return
	}
	keys, unique, rings, groups, misses := plan.keys, plan.unique, plan.rings, plan.groups, plan.misses

	// Seules les isochrones à calculer sont facturées, avant l'appel externe.
	refund, ok := h.charge(w, r, fmt.Sprintf("Recherche (%d isochrone(s))", misses),
		func(s models.BillingSettings) int64 { return s.PricePerOriginCents * int64(misses) })
	if !ok {
		return
	}
	succeeded := false
	defer func() {
		if !succeeded {
			refund()
		}
	}()

	if misses > 0 {
		computed, err := h.computeIsochrones(ctx, groups)
		if err != nil {
			log.Error("search: isochrone computation failed", "misses", misses, "error", err)
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		if err := h.Store.PutCachedIsochrones(context.WithoutCancel(ctx), computed, h.isoCacheTTL(), h.IsoCacheMax); err != nil {
			log.Warn("search: isochrone cache write failed", "error", err)
		}
		for k, ring := range computed {
			rings[k] = ring
		}
	}

	for i := range resp.Sources {
		resp.Sources[i].Isochrone = rings[keys[i]]
	}

	zoneGroups := isochroneGroups(body.Criteres, resp.Sources, keys, rings)
	zoneWKT, zoneGJ, empty, err := h.Store.IntersectUnions(ctx, zoneGroups)
	if err != nil {
		log.Error("search: isochrones combination failed", "error", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if empty {
		log.Info("search: empty zone", "count_sources", len(resp.Sources), "count_criteres", len(zoneGroups))
		succeeded = true
		writeJSON(w, http.StatusOK, resp)
		return
	}
	resp.ZoneBrute = zoneGJ

	if body.FiltresZone.Actif() {
		zoneWKT, zoneGJ, empty, err = h.Store.FilterZone(ctx, zoneWKT, body.FiltresZone)
		if err != nil {
			log.Error("search: zone filtering failed", "error", err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if empty {
			log.Info("search: zone emptied by filters", "count_sources", len(resp.Sources))
			succeeded = true
			writeJSON(w, http.StatusOK, resp)
			return
		}
	}
	resp.Zone = zoneGJ

	if err := h.fillZone(ctx, &resp, zoneWKT, body.Criteres); err != nil {
		log.Error("search: zone content lookup failed", "error", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	log.Info("search done",
		"centre", body.Centre,
		"rayon_km", body.RayonKm,
		"criteres", body.Criteres,
		"count_sources", len(resp.Sources),
		"isochrones_computed", misses,
		"isochrones_cached", len(unique)-misses,
		"count", len(resp.Etablissements),
		"count_communes", len(resp.Communes),
		"count_quartiers", len(resp.Immobilier.Quartiers),
		"count_logements_sociaux", len(resp.LogementsSociaux),
	)
	succeeded = true
	writeJSON(w, http.StatusOK, resp)
}

// estimateSearch gère POST /etablissements/estimation : même corps que
// POST /etablissements, mais sans calcul ni débit. Elle renvoie le nombre de
// sources et d'isochrones à calculer, et le coût de la recherche (nul si la
// facturation est désactivée ou pour un administrateur), à confirmer par
// l'interface au-delà de h.SearchCostWarningCents.
func (h *Handlers) estimateSearch(w http.ResponseWriter, r *http.Request) {
	log := logger(r.Context())
	user, ok := currentUser(w, r)
	if !ok {
		return
	}
	plan, ok := h.prepareSearch(w, r, "estimate")
	if !ok {
		return
	}
	est := models.EstimationRecherche{
		Sources:            len(plan.sources),
		Isochrones:         len(plan.unique),
		ACalculer:          plan.misses,
		SoldeCents:         user.BalanceCents,
		Gratuit:            !h.billingEnabled() || user.Perms == models.PermsAdmin,
		AvertissementCents: h.SearchCostWarningCents,
		CriteresVides:      plan.criteresVides,
	}
	if !est.Gratuit {
		settings, err := h.Store.GetBillingSettings(r.Context())
		if err != nil {
			log.Error("estimate: could not read billing settings", "error", err)
			writeError(w, http.StatusInternalServerError, "could not read billing settings")
			return
		}
		est.PrixUnitaireCents = settings.PricePerOriginCents
		est.CoutCents = settings.PricePerOriginCents * int64(est.ACalculer)
	}
	log.Info("search estimated", "centre", plan.body.Centre, "rayon_km", plan.body.RayonKm,
		"count_sources", est.Sources, "isochrones", est.Isochrones, "a_calculer", est.ACalculer, "cout_cents", est.CoutCents)
	writeJSON(w, http.StatusOK, est)
}

// compareKeys ordonne les clés de cache (pour les dédoublonner).
func compareKeys(a, b models.IsochroneKey) int {
	switch {
	case a.Transp != b.Transp:
		return strings.Compare(a.Transp, b.Transp)
	case a.Dist != b.Dist:
		return a.Dist - b.Dist
	case a.LonE5 != b.LonE5:
		return a.LonE5 - b.LonE5
	default:
		return a.LatE5 - b.LatE5
	}
}

// isochroneGroups renvoie, pour chaque critère (dans l'ordre), les WKT des
// isochrones distinctes de ses sources : la zone est l'intersection des
// unions de ces groupes. keys[i] est la clé de cache de sources[i].
func isochroneGroups(criteres []models.CritereNature, sources []models.Source, keys []models.IsochroneKey, rings map[models.IsochroneKey][][2]float64) [][]string {
	groups := make([][]string, 0, len(criteres))
	for _, c := range criteres {
		seen := map[models.IsochroneKey]bool{}
		var wkts []string
		for i, s := range sources {
			if s.Critere != c.Nature || seen[keys[i]] {
				continue
			}
			seen[keys[i]] = true
			wkts = append(wkts, geo.RingWKT(rings[keys[i]]))
		}
		groups = append(groups, wkts)
	}
	return groups
}

// computeIsochrones calcule les isochrones de chaque groupe, en parallèle.
func (h *Handlers) computeIsochrones(ctx context.Context, groups []*isoGroup) (map[models.IsochroneKey][][2]float64, error) {
	results := make([][]models.Isochrone, len(groups))
	g, gctx := errgroup.WithContext(ctx)
	for i, grp := range groups {
		g.Go(func() error {
			isos, err := h.Isochron.CalcIsochrones(gctx, grp.pts, grp.dist, grp.transp)
			if err != nil {
				return err
			}
			if len(isos) != len(grp.pts) {
				return fmt.Errorf("got %d isochrones for %d locations", len(isos), len(grp.pts))
			}
			results[i] = isos
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	out := map[models.IsochroneKey][][2]float64{}
	for i, grp := range groups {
		for j, k := range grp.keys {
			out[k] = results[i][j].Geometry
		}
	}
	return out, nil
}

// fillZone lit le contenu de la zone : établissements satisfaisant les
// critères, communes, prix immobiliers et logements sociaux.
func (h *Handlers) fillZone(ctx context.Context, resp *models.ListeEtablissementsAvecIsochrone, zoneWKT string, criteres []models.CritereNature) error {
	etabs, err := h.Store.FindEtablissementsInZone(ctx, zoneWKT, criteres)
	if err != nil {
		return err
	}
	if etabs != nil {
		resp.Etablissements = etabs
	}

	communes, err := h.Store.FindCommunesInZone(ctx, zoneWKT)
	if err != nil {
		return err
	}
	if communes != nil {
		resp.Communes = communes
	}

	codes := make([]string, len(resp.Communes))
	for i, c := range resp.Communes {
		codes[i] = c.CodeCommune
	}
	immo, err := h.Store.FindImmobilierInZone(ctx, zoneWKT, codes)
	if err != nil {
		return err
	}
	if immo.Communes == nil {
		immo.Communes = []models.CommuneImmobilier{}
	}
	if immo.Quartiers == nil {
		immo.Quartiers = []models.QuartierImmobilier{}
	}
	resp.Immobilier = immo

	logements, err := h.Store.FindLogementsSociauxInZone(ctx, zoneWKT)
	if err != nil {
		return err
	}
	if logements != nil {
		resp.LogementsSociaux = logements
	}
	return nil
}

// irisPattern valide un code IRIS (9 caractères : code commune, dont 2A/2B,
// et numéro d'IRIS).
var irisPattern = regexp.MustCompile(`^[0-9][0-9AB][0-9]{7}$`)

// quartierProximite gère POST /quartiers/{iris}/proximite : fiche du quartier
// et établissement le plus proche pour chaque critère (non facturé).
func (h *Handlers) quartierProximite(w http.ResponseWriter, r *http.Request) {
	iris := chi.URLParam(r, "iris")
	log := logger(r.Context()).With("iris", iris)

	var body models.ProximiteRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		log.Warn("proximite: invalid JSON body", "error", err)
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	verr := validateCriteres(body.Criteres, false)
	switch {
	case !irisPattern.MatchString(iris):
		verr = errors.New("invalid IRIS code")
	case len(body.Criteres) > len(models.Natures):
		verr = fmt.Errorf("at most %d criteres are allowed", len(models.Natures))
	}
	if verr != nil {
		log.Warn("proximite: invalid parameters", "error", verr)
		writeError(w, http.StatusUnprocessableEntity, verr.Error())
		return
	}

	q, err := h.Store.QuartierProximite(r.Context(), iris, body.Criteres)
	if err != nil {
		log.Error("proximite: lookup failed", "error", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if q == nil {
		log.Warn("proximite: unknown IRIS")
		writeError(w, http.StatusNotFound, "IRIS '"+iris+"' not found")
		return
	}
	writeJSON(w, http.StatusOK, q)
}

// validLonLat indique si p ([lon, lat]) est une position WGS84 valide.
func validLonLat(p [2]float64) bool {
	return p[0] >= -180 && p[0] <= 180 && p[1] >= -90 && p[1] <= 90
}

// itineraire gère POST /itineraire : trajet routier entre deux positions
// (fiche d'un quartier vers ses établissements les plus proches ; non facturé).
func (h *Handlers) itineraire(w http.ResponseWriter, r *http.Request) {
	log := logger(r.Context())

	var body models.ItineraireRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		log.Warn("itineraire: invalid JSON body", "error", err)
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	var verr error
	switch {
	case !slices.Contains(models.Transports, body.Transp):
		verr = fmt.Errorf("transp must be one of %v", models.Transports)
	case !validLonLat(body.Depart) || !validLonLat(body.Arrivee):
		verr = errors.New("depart and arrivee must be valid [lon, lat] positions")
	}
	if verr != nil {
		log.Warn("itineraire: invalid parameters", "error", verr)
		writeError(w, http.StatusUnprocessableEntity, verr.Error())
		return
	}

	it, err := h.Routes.CalcItineraire(r.Context(), body.Depart, body.Arrivee, body.Transp)
	if err != nil {
		log.Error("itineraire: routing failed", "error", err)
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, it)
}
