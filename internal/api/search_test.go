package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/umaneldi/douceville/internal/geo"
	"github.com/umaneldi/douceville/internal/models"
)

// source construit un établissement source de la nature donnée à la
// position (lon, lat), retenu pour le critère de même nature, avec le temps et
// le mode de transport de ce critère.
func source(uai, nature string, lon, lat, dist float64, transp string) models.Source {
	return models.Source{
		Etablissement: models.Etablissement{UAI: uai, Nature: nature,
			Position: json.RawMessage(fmt.Sprintf(`{"type":"Point","coordinates":[%v,%v]}`, lon, lat))},
		Critere: nature, Dist: dist, Transp: transp,
	}
}

// primaire construit une école primaire source retenue pour le critère donné.
func primaire(uai, critere string, lon, lat, dist float64, transp string) models.Source {
	s := source(uai, models.NaturePrimaire, lon, lat, dist, transp)
	s.Critere = critere
	return s
}

// searchHandlers renvoie des handlers de recherche (sans facturation) dont le
// store propose deux lycées (20 min en voiture) et un collège (10 min à pied).
func searchHandlers(t *testing.T) (*Handlers, mockStore, *[]isoCall) {
	t.Helper()
	st := alice(t, true)
	st.sources = []models.Source{
		source("L1", "Lycée", 1.40, 43.60, 1200, "driving-car"),
		source("L2", "Lycée", 1.45, 43.65, 1200, "driving-car"),
		source("C1", "Collège", 1.42, 43.61, 600, "foot-walking"),
	}
	st.zone = []models.Etablissement{{UAI: "L1"}, {UAI: "X"}}
	st.communes = []models.CommuneSecurite{{CodeCommune: "31555", Nom: "Toulouse"}}
	st.immo = models.ImmobilierZone{Quartiers: []models.QuartierImmobilier{{Iris: "315550101"}}}
	st.logements = []models.LogementSocialPosition{{Lon: 1.44, Lat: 43.6, NbLogements: 5}}
	st.cache = &isoCache{}
	calls := &[]isoCall{}
	h := &Handlers{
		Store:    st,
		Isochron: mockIso{calls: calls},
		Geocoder: mockGeo{res: geo.BANResult{Longitude: 1.44, Latitude: 43.6}},
	}
	return h, st, calls
}

const searchTwoNatures = `{"centre":"Toulouse","rayon_km":10,"criteres":[
	{"nature":"Lycée","secteur":["public","privé"],"taux_min":85,"taux_max":100,"dist":1200,"transp":"driving-car"},
	{"nature":"Collège","secteur":["public"],"dist":600,"transp":"foot-walking"}]}`

// decodeSearch décode une réponse de recherche.
func decodeSearch(t *testing.T, body string) models.ListeEtablissementsAvecIsochrone {
	t.Helper()
	var got models.ListeEtablissementsAvecIsochrone
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	return got
}

func TestSearch_Nominal(t *testing.T) {
	h, st, calls := searchHandlers(t)
	var criteres []models.CritereNature
	st.criteres = &criteres
	var inter [][][]string
	st.interCalls = &inter
	h.Store = st

	rr := callAs(t, h, st, http.MethodPost, "/api/etablissements", searchTwoNatures)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rr.Code, rr.Body.String())
	}
	got := decodeSearch(t, rr.Body.String())

	// Une requête ORS par couple (temps, mode), avec les paramètres du critère.
	if len(*calls) != 2 {
		t.Fatalf("ORS calls = %+v, want 2 (one per dist/transp)", *calls)
	}
	byTransp := map[string]isoCall{}
	for _, c := range *calls {
		byTransp[c.transp] = c
	}
	if c := byTransp["driving-car"]; c.dist != 1200 || len(c.lonlats) != 2 {
		t.Errorf("lycées call = %+v, want 2 positions at 1200 s", c)
	}
	if c := byTransp["foot-walking"]; c.dist != 600 || len(c.lonlats) != 1 || c.lonlats[0] != [2]float64{1.42, 43.61} {
		t.Errorf("collège call = %+v, want [1.42 43.61] at 600 s", c)
	}
	if st.cache.puts != 1 || len(st.cache.rings) != 3 {
		t.Errorf("cache: %d puts, %d rings; want 1 put of 3 isochrones", st.cache.puts, len(st.cache.rings))
	}
	// Un groupe par critère, dans l'ordre : 2 isochrones de lycée, 1 de collège.
	if len(inter) != 1 || len(inter[0]) != 2 || len(inter[0][0]) != 2 || len(inter[0][1]) != 1 {
		t.Fatalf("IntersectUnions calls = %v, want one call with groups of 2 and 1 polygons", inter)
	}
	if inter[0][0][0] == inter[0][0][1] || !strings.HasPrefix(inter[0][1][0], "POLYGON((1.420000 43.610000") {
		t.Errorf("groups = %v, want two distinct lycée rings then the collège ring", inter[0])
	}

	if got.Centre != (models.Centre{Lon: 1.44, Lat: 43.6, RayonKm: 10}) {
		t.Errorf("centre = %+v", got.Centre)
	}
	if len(got.Sources) != 3 || len(got.CriteresVides) != 0 {
		t.Fatalf("sources = %+v, criteres_vides = %v", got.Sources, got.CriteresVides)
	}
	for _, s := range got.Sources {
		ll, _ := s.LonLat()
		if len(s.Isochrone) == 0 || s.Isochrone[0] != ll {
			t.Errorf("source %s isochrone = %v, want its own ring", s.UAI, s.Isochrone)
		}
	}
	if string(got.Zone) == "null" || len(got.Zone) == 0 || string(got.ZoneBrute) != string(got.Zone) {
		t.Errorf("zone = %s, zone_brute = %s; want the same geometry (no filter)", got.Zone, got.ZoneBrute)
	}
	if len(got.Etablissements) != 2 || len(got.Communes) != 1 || len(got.Immobilier.Quartiers) != 1 || len(got.LogementsSociaux) != 1 {
		t.Errorf("zone content = %+v", got)
	}
	// Critères normalisés transmis aux requêtes de zone.
	if len(criteres) != 2 || criteres[0].TauxMin != 85 || criteres[1].TauxMax != 100 || criteres[1].Secteur[0] != "public" {
		t.Errorf("criteres = %+v", criteres)
	}
}

// Deux sources d'une même nature à la même position (même temps, même mode)
// partagent une isochrone : elle n'apparaît qu'une fois dans le groupe.
func TestSearch_GroupsDeduplicateIsochrones(t *testing.T) {
	h, st, _ := searchHandlers(t)
	st.sources = append(st.sources, source("L3", "Lycée", 1.40, 43.60, 1200, "driving-car"))
	var inter [][][]string
	st.interCalls = &inter
	h.Store = st

	rr := callAs(t, h, st, http.MethodPost, "/api/etablissements", searchTwoNatures)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rr.Code, rr.Body.String())
	}
	if len(inter) != 1 || len(inter[0]) != 2 || len(inter[0][0]) != 2 || len(inter[0][1]) != 1 {
		t.Errorf("IntersectUnions calls = %v, want groups of 2 and 1 polygons", inter)
	}
	if got := decodeSearch(t, rr.Body.String()); len(got.Sources) != 4 {
		t.Errorf("sources = %d, want 4", len(got.Sources))
	}
}

// Une école primaire retenue pour les critères maternelle et élémentaire
// compte dans le groupe de chacun (avec l'isochrone de chaque critère), et
// aucun de ces critères n'est vide.
func TestSearch_PrimaireSourceServesBothCriteres(t *testing.T) {
	h, st, calls := searchHandlers(t)
	st.sources = []models.Source{
		primaire("P", "Maternelle", 1.40, 43.60, 600, "cycling-regular"),
		primaire("P", "Elementaire", 1.40, 43.60, 900, "cycling-regular"),
		source("E", "Elementaire", 1.45, 43.65, 900, "cycling-regular"),
	}
	var inter [][][]string
	st.interCalls = &inter
	h.Store = st

	body := `{"centre":"Toulouse","rayon_km":20,"criteres":[
		{"nature":"Maternelle","dist":600,"transp":"cycling-regular"},
		{"nature":"Elementaire","dist":900,"transp":"cycling-regular"}]}`
	rr := callAs(t, h, st, http.MethodPost, "/api/etablissements", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rr.Code, rr.Body.String())
	}
	got := decodeSearch(t, rr.Body.String())
	if len(got.CriteresVides) != 0 || string(got.Zone) == "null" {
		t.Fatalf("criteres_vides = %v, zone = %s; want no empty critère and a zone", got.CriteresVides, got.Zone)
	}
	// Maternelle : l'isochrone de P à 600 s ; élémentaire : P à 900 s et E.
	if len(inter) != 1 || len(inter[0]) != 2 || len(inter[0][0]) != 1 || len(inter[0][1]) != 2 {
		t.Fatalf("IntersectUnions calls = %v, want groups of 1 and 2 polygons", inter)
	}
	if len(*calls) != 2 {
		t.Errorf("ORS calls = %+v, want 2 (600 s and 900 s)", *calls)
	}
	if p := got.Sources[0]; p.Nature != models.NaturePrimaire || p.Critere != "Maternelle" {
		t.Errorf("source P = %+v, want nature Primaire and critere Maternelle in the JSON", p)
	}
}

func TestSearch_AllCachedIsFree(t *testing.T) {
	h, st, _ := billed(t, models.PermsGranted, 100)
	st.cache = &isoCache{}
	h.Store = st
	h.Isochron = mockIso{calls: calls0()}

	if rr := call(t, h, http.MethodPost, "/api/etablissements", threeOrigins); rr.Code != http.StatusOK {
		t.Fatalf("first search: status = %d (%s)", rr.Code, rr.Body.String())
	}
	if st.bill.balances["u1"] != 85 {
		t.Fatalf("first search: balance = %d, want 85 (3 isochrones × 5)", st.bill.balances["u1"])
	}

	calls := &[]isoCall{}
	h.Isochron = mockIso{calls: calls}
	rr := call(t, h, http.MethodPost, "/api/etablissements", threeOrigins)
	if rr.Code != http.StatusOK {
		t.Fatalf("second search: status = %d", rr.Code)
	}
	if len(*calls) != 0 || st.bill.balances["u1"] != 85 || len(st.bill.txs) != 1 {
		t.Errorf("cached search: %d ORS calls, balance %d, %d txs; want 0, 85, 1", len(*calls), st.bill.balances["u1"], len(st.bill.txs))
	}
	if got := decodeSearch(t, rr.Body.String()); len(got.Sources[0].Isochrone) == 0 {
		t.Errorf("cached isochrone not returned: %+v", got.Sources[0])
	}
}

// calls0 renvoie un journal d'appels vide.
func calls0() *[]isoCall { return &[]isoCall{} }

func TestSearch_PartialCacheChargesMissesOnly(t *testing.T) {
	h, st, _ := billed(t, models.PermsGranted, 100)
	st.cache = &isoCache{rings: map[models.IsochroneKey][][2]float64{
		models.NewIsochroneKey([2]float64{1.40, 43.60}, 600, "driving-car"): {{0, 0}, {1, 0}, {0, 1}, {0, 0}},
	}}
	// Deux sources au même endroit (même clé) : une seule isochrone.
	st.sources = append(st.sources, source("D", "Lycée", 1.42, 43.62, 600, "driving-car"))
	h.Store = st
	calls := calls0()
	h.Isochron = mockIso{calls: calls}

	if rr := call(t, h, http.MethodPost, "/api/etablissements", threeOrigins); rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if len(*calls) != 1 || len((*calls)[0].lonlats) != 2 {
		t.Errorf("ORS calls = %+v, want one call for the 2 missing positions", *calls)
	}
	if st.bill.balances["u1"] != 90 {
		t.Errorf("balance = %d, want 90 (2 isochrones × 5)", st.bill.balances["u1"])
	}
}

func TestSearch_Validation(t *testing.T) {
	h, st, calls := searchHandlers(t)
	crit := func(c string) string { return `{"centre":"a","rayon_km":5,"criteres":[` + c + `]}` }
	for name, body := range map[string]string{
		"centre manquant":      `{"rayon_km":5,"criteres":[{"nature":"Lycée","dist":600}]}`,
		"centre vide":          `{"centre":"  ","rayon_km":5,"criteres":[{"nature":"Lycée","dist":600}]}`,
		"rayon nul":            `{"centre":"a","criteres":[{"nature":"Lycée","dist":600}]}`,
		"rayon trop grand":     `{"centre":"a","rayon_km":51,"criteres":[{"nature":"Lycée","dist":600}]}`,
		"aucun critère":        `{"centre":"a","rayon_km":5,"criteres":[]}`,
		"nature inconnue":      crit(`{"nature":"Université","dist":600}`),
		"nature en double":     crit(`{"nature":"Lycée","dist":600},{"nature":"Lycée","dist":900}`),
		"trop de critères":     crit(`{"nature":"Lycée","dist":600},{"nature":"Collège","dist":600},{"nature":"Maternelle","dist":600},{"nature":"Elementaire","dist":600},{"nature":"Lycée","dist":600}`),
		"secteur inconnu":      crit(`{"nature":"Lycée","dist":600,"secteur":["confessionnel"]}`),
		"taux inversé":         crit(`{"nature":"Lycée","dist":600,"taux_min":90,"taux_max":80}`),
		"taux > 100":           crit(`{"nature":"Lycée","dist":600,"taux_max":120}`),
		"taux négatif":         crit(`{"nature":"Lycée","dist":600,"taux_min":-1,"taux_max":50}`),
		"temps trop court":     crit(`{"nature":"Lycée","dist":30}`),
		"temps trop long":      crit(`{"nature":"Lycée","dist":7200}`),
		"transport inconnu":    crit(`{"nature":"Lycée","dist":600,"transp":"teleport"}`),
		"prix inversé":         `{"centre":"a","rayon_km":5,"criteres":[{"nature":"Lycée","dist":600}],"prix_m2_min":5000,"prix_m2_max":3000}`,
		"prix négatif":         `{"centre":"a","rayon_km":5,"criteres":[{"nature":"Lycée","dist":600}],"prix_m2_min":-1}`,
		"fiscalité négative":   `{"centre":"a","rayon_km":5,"criteres":[{"nature":"Lycée","dist":600}],"fiscalite_max":-1}`,
		"sécurité hors bornes": `{"centre":"a","rayon_km":5,"criteres":[{"nature":"Lycée","dist":600}],"securite_min":101}`,
	} {
		t.Run(name, func(t *testing.T) {
			if rr := callAs(t, h, st, http.MethodPost, "/api/etablissements", body); rr.Code != http.StatusUnprocessableEntity {
				t.Errorf("status = %d, want 422 (%s)", rr.Code, rr.Body.String())
			}
		})
	}
	if rr := callAs(t, h, st, http.MethodPost, "/api/etablissements", `{`); rr.Code != http.StatusBadRequest {
		t.Errorf("invalid JSON: status = %d, want 400", rr.Code)
	}
	if len(*calls) != 0 {
		t.Errorf("no ORS call expected on invalid input, got %d", len(*calls))
	}
}

func TestSearch_Access(t *testing.T) {
	h, st, _ := searchHandlers(t)
	// Sans jeton : 403 (parité avec l'ancien backend) ; jeton invalide : 401.
	for _, path := range []string{"/api/etablissements", "/api/quartiers/315550101/proximite"} {
		for token, want := range map[string]int{"": http.StatusForbidden, "Bearer invalid": http.StatusUnauthorized} {
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
			if token != "" {
				req.Header.Set("Authorization", token)
			}
			rr := httptest.NewRecorder()
			NewRouter(testSecret, h, st).ServeHTTP(rr, req)
			if rr.Code != want {
				t.Errorf("%s with token %q: status = %d, want %d", path, token, rr.Code, want)
			}
		}
	}
	// Compte non validé : 403.
	st.user.Perms = models.PermsVerified
	if rr := callAs(t, h, st, http.MethodPost, "/api/etablissements", searchTwoNatures); rr.Code != http.StatusForbidden {
		t.Errorf("verified: status = %d, want 403", rr.Code)
	}
	if rr := callAs(t, h, st, http.MethodPost, "/api/quartiers/315550101/proximite", `{"criteres":[]}`); rr.Code != http.StatusForbidden {
		t.Errorf("proximite verified: status = %d, want 403", rr.Code)
	}
}

func TestSearch_UpstreamErrors(t *testing.T) {
	h, st, _ := searchHandlers(t)
	h.Geocoder = mockGeo{err: errors.New("BAN down")}
	if rr := callAs(t, h, st, http.MethodPost, "/api/etablissements", searchTwoNatures); rr.Code != http.StatusBadGateway {
		t.Errorf("geocoding error: status = %d, want 502", rr.Code)
	}

	h, st, _ = searchHandlers(t)
	h.Isochron = mockIso{err: errors.New("ORS down")}
	if rr := callAs(t, h, st, http.MethodPost, "/api/etablissements", searchTwoNatures); rr.Code != http.StatusBadGateway {
		t.Errorf("ORS error: status = %d, want 502", rr.Code)
	}
	if st.cache.puts != 0 {
		t.Errorf("nothing must be cached on error, got %d puts", st.cache.puts)
	}
}

func TestSearch_CacheFailuresAreIgnored(t *testing.T) {
	h, st, calls := searchHandlers(t)
	st.cache.getErr = errors.New("cache read down")
	st.cache.putErr = errors.New("cache write down")
	h.Store = st
	rr := callAs(t, h, st, http.MethodPost, "/api/etablissements", searchTwoNatures)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rr.Code, rr.Body.String())
	}
	if len(*calls) != 2 {
		t.Errorf("ORS calls = %d, want 2 (cache unavailable)", len(*calls))
	}
	if got := decodeSearch(t, rr.Body.String()); len(got.Zone) == 0 || string(got.Zone) == "null" {
		t.Errorf("zone = %s", got.Zone)
	}
}

func TestSearch_EmptyCases(t *testing.T) {
	// Un critère sans source : zone vide, sans calcul ni facturation.
	h, st, _ := billed(t, models.PermsGranted, 100)
	calls := calls0()
	h.Isochron = mockIso{calls: calls}
	body := `{"centre":"a","rayon_km":5,"criteres":[{"nature":"Lycée","dist":600},{"nature":"Maternelle","dist":300}]}`
	rr := call(t, h, http.MethodPost, "/api/etablissements", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	got := decodeSearch(t, rr.Body.String())
	if fmt.Sprint(got.CriteresVides) != "[Maternelle]" || len(got.Sources) != 3 || string(got.Zone) != "null" {
		t.Errorf("criteres_vides = %v, sources = %d, zone = %s", got.CriteresVides, len(got.Sources), got.Zone)
	}
	if len(*calls) != 0 || len(st.bill.txs) != 0 {
		t.Errorf("no ORS call nor charge expected: %d calls, txs %v", len(*calls), st.bill.txs)
	}

	// Aucune source du tout : chaque critère est vide.
	h, st, _ = searchHandlers(t)
	st.sources = nil
	h.Store = st
	rr = callAs(t, h, st, http.MethodPost, "/api/etablissements", searchTwoNatures)
	if got := decodeSearch(t, rr.Body.String()); rr.Code != 200 || fmt.Sprint(got.CriteresVides) != "[Lycée Collège]" ||
		!strings.Contains(rr.Body.String(), `"sources":[]`) || !strings.Contains(rr.Body.String(), `"etablissements":[]`) {
		t.Errorf("no source: status = %d, body = %s", rr.Code, rr.Body.String())
	}

	// Intersection vide : sources et isochrones renvoyées, zone nulle, payée.
	h, st, _ = billed(t, models.PermsGranted, 100)
	st.interEmpty = true
	h.Store = st
	rr = call(t, h, http.MethodPost, "/api/etablissements", threeOrigins)
	got = decodeSearch(t, rr.Body.String())
	if rr.Code != 200 || string(got.Zone) != "null" || string(got.ZoneBrute) != "null" || len(got.Sources) != 3 ||
		len(got.Sources[0].Isochrone) == 0 || len(got.Communes) != 0 {
		t.Errorf("empty intersection: status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if st.bill.balances["u1"] != 85 {
		t.Errorf("empty intersection still computed isochrones: balance = %d, want 85", st.bill.balances["u1"])
	}

	// Zone vidée par les filtres : zone_brute renseignée, zone nulle.
	h, st, _ = searchHandlers(t)
	st.filterEmpty = true
	filterCalls := 0
	st.filterCalls = &filterCalls
	h.Store = st
	rr = callAs(t, h, st, http.MethodPost, "/api/etablissements",
		`{"centre":"a","rayon_km":5,"criteres":[{"nature":"Lycée","dist":1200}],"securite_min":80}`)
	got = decodeSearch(t, rr.Body.String())
	if rr.Code != 200 || filterCalls != 1 || string(got.Zone) != "null" || len(got.ZoneBrute) == 0 ||
		string(got.ZoneBrute) == "null" || len(got.Etablissements) != 0 {
		t.Errorf("filtered out: status = %d, filter calls = %d, body = %s", rr.Code, filterCalls, rr.Body.String())
	}
}

func TestSearch_Filters(t *testing.T) {
	h, st, _ := searchHandlers(t)
	filterCalls := 0
	st.filterCalls = &filterCalls
	h.Store = st
	// Sans filtre : FilterZone n'est pas appelé.
	if rr := callAs(t, h, st, http.MethodPost, "/api/etablissements", searchTwoNatures); rr.Code != 200 || filterCalls != 0 {
		t.Fatalf("no filter: status = %d, filter calls = %d", rr.Code, filterCalls)
	}
	body := `{"centre":"a","rayon_km":5,"criteres":[{"nature":"Lycée","dist":1200}],
		"prix_m2_min":2000,"prix_m2_max":4000,"securite_min":50,"fiscalite_max":45}`
	rr := callAs(t, h, st, http.MethodPost, "/api/etablissements", body)
	got := decodeSearch(t, rr.Body.String())
	if rr.Code != 200 || filterCalls != 1 || string(got.Zone) == string(got.ZoneBrute) || len(got.Etablissements) != 2 {
		t.Errorf("filters: status = %d, filter calls = %d, zone = %s, brute = %s", rr.Code, filterCalls, got.Zone, got.ZoneBrute)
	}
}

func TestSearch_AdminNotCharged(t *testing.T) {
	h, st, _ := billed(t, models.PermsAdmin, 0)
	h.IsoCacheTTL = time.Hour // durée de cache explicite
	if rr := call(t, h, http.MethodPost, "/api/etablissements", threeOrigins); rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if len(st.bill.txs) != 0 {
		t.Errorf("admin charged: %v", st.bill.txs)
	}
}

func TestSearch_InvalidSourcePosition(t *testing.T) {
	h, st, _ := searchHandlers(t)
	st.sources = []models.Source{{Etablissement: models.Etablissement{UAI: "Z", Nature: "Lycée"}, Critere: "Lycée", Dist: 600, Transp: "driving-car"}}
	h.Store = st
	body := `{"centre":"a","rayon_km":5,"criteres":[{"nature":"Lycée","dist":600}]}`
	if rr := callAs(t, h, st, http.MethodPost, "/api/etablissements", body); rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rr.Code)
	}
}

// wrongCountIso renvoie une isochrone de moins que de positions.
type wrongCountIso struct{ mockIso }

func (w wrongCountIso) CalcIsochrones(ctx context.Context, lonlats [][2]float64, dist float64, transp string) ([]models.Isochrone, error) {
	isos, err := w.mockIso.CalcIsochrones(ctx, lonlats, dist, transp)
	return isos[1:], err
}

func TestSearch_IsochroneCountMismatch(t *testing.T) {
	h, st, _ := searchHandlers(t)
	h.Isochron = wrongCountIso{}
	if rr := callAs(t, h, st, http.MethodPost, "/api/etablissements", searchTwoNatures); rr.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rr.Code)
	}
}

func TestQuartierProximite(t *testing.T) {
	st := alice(t, true)
	d := 850.0
	uai := "L1"
	st.proximite = &models.QuartierProximite{Iris: "315550101", Nom: "Capitole",
		Proches: []models.Proche{{Nature: "Lycée", DistanceM: &d, UAI: &uai}, {Nature: "Collège"}}}
	var criteres []models.CritereNature
	st.criteres = &criteres
	h := &Handlers{Store: st}

	rr := callAs(t, h, st, http.MethodPost, "/api/quartiers/315550101/proximite",
		`{"criteres":[{"nature":"Lycée","secteur":["public"],"taux_min":80},{"nature":"Collège"}]}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rr.Code, rr.Body.String())
	}
	var got models.QuartierProximite
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil || got.Nom != "Capitole" || len(got.Proches) != 2 ||
		*got.Proches[0].DistanceM != 850 || got.Proches[1].DistanceM != nil {
		t.Errorf("body = %s (%v)", rr.Body.String(), err)
	}
	// Critères normalisés : taux_max absent vaut 100.
	if len(criteres) != 2 || criteres[0].TauxMin != 80 || criteres[0].TauxMax != 100 || criteres[1].TauxMax != 100 {
		t.Errorf("criteres = %+v", criteres)
	}

	// Corse (2A) accepté ; IRIS inconnu : 404.
	st.proximite = nil
	h.Store = st
	if rr := callAs(t, h, st, http.MethodPost, "/api/quartiers/2A0040101/proximite", `{"criteres":[]}`); rr.Code != http.StatusNotFound {
		t.Errorf("unknown IRIS: status = %d, want 404", rr.Code)
	}

	for name, tc := range map[string]struct {
		iris, body string
		want       int
	}{
		"JSON invalide":    {"315550101", `{`, 400},
		"code IRIS":        {"31555", `{"criteres":[]}`, 422},
		"code IRIS lettre": {"31555010X", `{"criteres":[]}`, 422},
		"nature inconnue":  {"315550101", `{"criteres":[{"nature":"Fac"}]}`, 422},
		"taux invalide":    {"315550101", `{"criteres":[{"nature":"Lycée","taux_min":50,"taux_max":20}]}`, 422},
		"trop de critères": {"315550101", `{"criteres":[{"nature":"Lycée"},{"nature":"Collège"},{"nature":"Maternelle"},{"nature":"Elementaire"},{"nature":"Lycée"}]}`, 422},
	} {
		t.Run(name, func(t *testing.T) {
			if rr := callAs(t, h, st, http.MethodPost, "/api/quartiers/"+tc.iris+"/proximite", tc.body); rr.Code != tc.want {
				t.Errorf("status = %d, want %d (%s)", rr.Code, tc.want, rr.Body.String())
			}
		})
	}
}

func TestCompareKeys(t *testing.T) {
	a := models.IsochroneKey{LonE5: 1, LatE5: 1, Dist: 600, Transp: "driving-car"}
	for _, tc := range []struct {
		b    models.IsochroneKey
		sign int
	}{
		{a, 0},
		{models.IsochroneKey{LonE5: 1, LatE5: 1, Dist: 600, Transp: "foot-walking"}, -1},
		{models.IsochroneKey{LonE5: 1, LatE5: 1, Dist: 300, Transp: "driving-car"}, 1},
		{models.IsochroneKey{LonE5: 2, LatE5: 1, Dist: 600, Transp: "driving-car"}, -1},
		{models.IsochroneKey{LonE5: 1, LatE5: 0, Dist: 600, Transp: "driving-car"}, 1},
	} {
		got := compareKeys(a, tc.b)
		if (got < 0 && tc.sign >= 0) || (got > 0 && tc.sign <= 0) || (got == 0 && tc.sign != 0) {
			t.Errorf("compareKeys(%+v, %+v) = %d, want sign %d", a, tc.b, got, tc.sign)
		}
	}
}
