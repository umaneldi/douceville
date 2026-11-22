package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/umaneldi/douceville/internal/geo"
	"github.com/umaneldi/douceville/internal/models"
)

// --- Mocks ---

type mockStore struct {
	etab      *models.Etablissement
	zone      []models.Etablissement
	communes  []models.CommuneSecurite
	immo      models.ImmobilierZone
	logements []models.LogementSocialPosition
	user      *models.DvUser
	rec       *models.UserRecord
	err       error
	createErr error
	// verified est renvoyé par VerifyEmail.
	verified *models.DvUser
	// revoked reçoit le jti passé à RevokeToken (si non nil).
	revoked *string
	// interEmpty est renvoyé par IntersectZones ; interCalls compte ses appels.
	interEmpty bool
	interCalls *int
}

func (m mockStore) IntersectZones(ctx context.Context, wkts []string) (string, json.RawMessage, bool, error) {
	if m.interCalls != nil {
		*m.interCalls++
	}
	if m.interEmpty {
		return "", nil, true, m.err
	}
	return "MULTIPOLYGON(((1 2,3 4,5 6,1 2)))", json.RawMessage(`{"type":"MultiPolygon","coordinates":[]}`), false, m.err
}

func (m mockStore) CreateEmailVerification(ctx context.Context, userID, tokenHash string, expiresAt time.Time) error {
	return m.err
}
func (m mockStore) VerifyEmail(ctx context.Context, tokenHash string) (*models.DvUser, error) {
	return m.verified, m.err
}
func (m mockStore) RevokeToken(ctx context.Context, jti string, expiresAt time.Time) error {
	if m.revoked != nil {
		*m.revoked = jti
	}
	return m.err
}
func (m mockStore) IsTokenRevoked(ctx context.Context, jti string) (bool, error) {
	return false, m.err
}

func (m mockStore) GetUser(ctx context.Context, id string) (*models.DvUser, error) {
	return m.user, m.err
}
func (m mockStore) GetUserByEmail(ctx context.Context, email string) (*models.UserRecord, error) {
	return m.rec, m.err
}
func (m mockStore) CreateUser(ctx context.Context, email, passwordHash string, firstName, lastName *string) (*models.DvUser, error) {
	if m.createErr != nil {
		return nil, m.createErr
	}
	return &models.DvUser{ID: "new-id", Email: &email}, nil
}
func (m mockStore) GetEtablissementByUAI(ctx context.Context, uai string) (*models.Etablissement, error) {
	return m.etab, m.err
}
func (m mockStore) FindEtablissementsInZone(ctx context.Context, wkt string, nature, secteur []string) ([]models.Etablissement, error) {
	return m.zone, m.err
}

func (m mockStore) FindCommunesInZone(ctx context.Context, wkt string) ([]models.CommuneSecurite, error) {
	return m.communes, m.err
}

func (m mockStore) FindImmobilierInZone(ctx context.Context, wkt string, codes []string) (models.ImmobilierZone, error) {
	return m.immo, m.err
}

func (m mockStore) FindLogementsSociauxInZone(ctx context.Context, wkt string) ([]models.LogementSocialPosition, error) {
	return m.logements, m.err
}

type mockIso struct{ iso models.Isochrone }

func (m mockIso) CalcIsochrone(ctx context.Context, lonlat [2]float64, dist float64, transp string) (models.Isochrone, error) {
	return m.iso, nil
}

type mockGeo struct{ res geo.BANResult }

func (m mockGeo) Geocode(ctx context.Context, query string) (geo.BANResult, error) {
	return m.res, nil
}

// --- Tests ---

func TestReadEtablissement_Found(t *testing.T) {
	h := &Handlers{Store: mockStore{etab: &models.Etablissement{UAI: "X42Y", Nom: "Test"}}}

	req := httptest.NewRequest(http.MethodGet, "/etablissement/X42Y", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("uai", "X42Y")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rr := httptest.NewRecorder()
	h.readEtablissement(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var got models.Etablissement
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.UAI != "X42Y" {
		t.Errorf("UAI = %q, want X42Y", got.UAI)
	}
}

func TestReadEtablissement_NotFound(t *testing.T) {
	h := &Handlers{Store: mockStore{etab: nil}}

	req := httptest.NewRequest(http.MethodGet, "/etablissement/ZZZ", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("uai", "ZZZ")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rr := httptest.NewRecorder()
	h.readEtablissement(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestEtablissementsInZone(t *testing.T) {
	score := 42
	t3, t4 := 3, 4
	dateMin, dateMax := "2021-07-01", "2025-06-30"
	h := &Handlers{
		Store: mockStore{
			zone: []models.Etablissement{{UAI: "A"}, {UAI: "B"}},
			communes: []models.CommuneSecurite{
				{CodeCommune: "31555", Nom: "Toulouse", Score: &score, Contour: json.RawMessage(`{"type":"MultiPolygon","coordinates":[]}`)},
				{CodeCommune: "31069", Nom: "Blagnac"},
			},
			immo: models.ImmobilierZone{
				DateMin: &dateMin,
				DateMax: &dateMax,
				Communes: []models.CommuneImmobilier{
					{CodeCommune: "31555", Prix: &models.PrixM2{PrixM2: 3400, Ventes: 1200}},
				},
				Quartiers: []models.QuartierImmobilier{
					{Iris: "315550101", CodeCommune: "31555", Nom: "Capitole",
						Prix:    &models.PrixM2{PrixM2: 5100, Ventes: 12},
						Contour: json.RawMessage(`{"type":"MultiPolygon","coordinates":[]}`)},
					{Iris: "315550102", CodeCommune: "31555", Nom: "Arnaud Bernard",
						Contour: json.RawMessage(`{"type":"MultiPolygon","coordinates":[]}`)},
				},
			},
			logements: []models.LogementSocialPosition{
				{Lon: 1.44, Lat: 43.6, NbLogements: 5, Logements: []models.LogementSocialGroupe{
					{NbPieces: &t3, Nb: 3},
					{NbPieces: &t4, Nb: 2},
				}},
			},
		},
		Isochron: mockIso{iso: models.Isochrone{Transp: "driving-car", Geometry: [][2]float64{{1, 2}, {3, 4}, {5, 6}}}},
		Geocoder: mockGeo{res: geo.BANResult{Longitude: 1.44, Latitude: 43.6}},
	}

	body := `{"adresse":"Toulouse","dist":600}`
	req := httptest.NewRequest(http.MethodPost, "/etablissements", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.etablissementsInZone(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var got models.ListeEtablissementsAvecIsochrone
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Etablissements) != 2 {
		t.Errorf("got %d etablissements, want 2", len(got.Etablissements))
	}
	if got.Isochrone.Transp != "driving-car" {
		t.Errorf("isochrone.transp = %q, want driving-car", got.Isochrone.Transp)
	}
	if len(got.Communes) != 2 {
		t.Fatalf("got %d communes, want 2", len(got.Communes))
	}
	if c := got.Communes[0]; c.Score == nil || *c.Score != 42 || c.Nom != "Toulouse" {
		t.Errorf("communes[0] = %+v, want Toulouse with score 42", c)
	}
	if got.Communes[1].Score != nil {
		t.Errorf("communes[1].score = %v, want null", *got.Communes[1].Score)
	}
	im := got.Immobilier
	if im.DateMin == nil || *im.DateMin != dateMin || im.DateMax == nil || *im.DateMax != dateMax {
		t.Errorf("immobilier dates = %v..%v, want %s..%s", im.DateMin, im.DateMax, dateMin, dateMax)
	}
	if len(im.Communes) != 1 || im.Communes[0].Prix == nil || im.Communes[0].Prix.PrixM2 != 3400 {
		t.Errorf("immobilier.communes = %+v, want 31555 prix 3400", im.Communes)
	}
	if len(im.Quartiers) != 2 || im.Quartiers[0].Prix == nil || im.Quartiers[0].Prix.Ventes != 12 || im.Quartiers[1].Prix != nil {
		t.Errorf("immobilier.quartiers = %+v, want Capitole 12 ventes, Arnaud Bernard sans prix", im.Quartiers)
	}
	if ls := got.LogementsSociaux; len(ls) != 1 || ls[0].NbLogements != 5 || len(ls[0].Logements) != 2 ||
		ls[0].Logements[1].NbPieces == nil || *ls[0].Logements[1].NbPieces != 4 {
		t.Errorf("logements_sociaux = %+v, want 1 position, 5 logements en 2 groupes", ls)
	}
}

// Sans commune dans la zone, le champ communes est un tableau vide (pas null).
func TestEtablissementsInZone_NoCommunes(t *testing.T) {
	h := &Handlers{
		Store:    mockStore{},
		Isochron: mockIso{iso: models.Isochrone{Geometry: [][2]float64{{1, 2}, {3, 4}, {5, 6}}}},
		Geocoder: mockGeo{},
	}
	req := httptest.NewRequest(http.MethodPost, "/etablissements", strings.NewReader(`{"adresse":"x","dist":600}`))
	rr := httptest.NewRecorder()
	h.etablissementsInZone(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"communes":[]`) {
		t.Errorf("body = %s, want \"communes\":[]", rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"logements_sociaux":[]`) {
		t.Errorf("body = %s, want \"logements_sociaux\":[]", rr.Body.String())
	}
}

func TestEtablissementsInZone_MissingAdresse(t *testing.T) {
	h := &Handlers{Store: mockStore{}, Isochron: mockIso{}, Geocoder: mockGeo{}}

	req := httptest.NewRequest(http.MethodPost, "/etablissements", strings.NewReader(`{"dist":600}`))
	rr := httptest.NewRecorder()
	h.etablissementsInZone(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rr.Code)
	}
}

func TestEtablissementsInZone_MultipleOrigines(t *testing.T) {
	calls := 0
	h := &Handlers{
		Store:    mockStore{zone: []models.Etablissement{{UAI: "A"}}, interCalls: &calls},
		Isochron: mockIso{iso: models.Isochrone{Transp: "foot-walking", Geometry: [][2]float64{{1, 2}, {3, 4}, {5, 6}}}},
		Geocoder: mockGeo{},
	}
	body := `{"origines":[{"adresse":"a","dist":600,"transp":"driving-car"},{"adresse":"b","dist":900,"transp":"foot-walking"}]}`
	req := httptest.NewRequest(http.MethodPost, "/etablissements", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.etablissementsInZone(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rr.Code, rr.Body.String())
	}
	var got models.ListeEtablissementsAvecIsochrone
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if calls != 1 {
		t.Errorf("IntersectZones called %d times, want 1", calls)
	}
	if len(got.Isochrones) != 2 {
		t.Errorf("got %d isochrones, want 2", len(got.Isochrones))
	}
	if len(got.Zone) == 0 || string(got.Zone) == "null" {
		t.Errorf("zone = %s, want intersection geometry", got.Zone)
	}
	if len(got.Etablissements) != 1 {
		t.Errorf("got %d etablissements, want 1", len(got.Etablissements))
	}
}

// Un seul point de départ : pas de calcul d'intersection, zone nulle.
func TestEtablissementsInZone_SingleOrigineNoIntersection(t *testing.T) {
	calls := 0
	h := &Handlers{
		Store:    mockStore{interCalls: &calls},
		Isochron: mockIso{iso: models.Isochrone{Geometry: [][2]float64{{1, 2}, {3, 4}, {5, 6}}}},
		Geocoder: mockGeo{},
	}
	req := httptest.NewRequest(http.MethodPost, "/etablissements", strings.NewReader(`{"origines":[{"adresse":"a","dist":600}]}`))
	rr := httptest.NewRecorder()
	h.etablissementsInZone(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if calls != 0 {
		t.Errorf("IntersectZones called %d times, want 0", calls)
	}
	if !strings.Contains(rr.Body.String(), `"zone":null`) {
		t.Errorf("body = %s, want \"zone\":null", rr.Body.String())
	}
}

// Intersection vide : 200 avec des listes vides, sans interroger les zones.
func TestEtablissementsInZone_EmptyIntersection(t *testing.T) {
	h := &Handlers{
		Store:    mockStore{zone: []models.Etablissement{{UAI: "A"}}, interEmpty: true},
		Isochron: mockIso{iso: models.Isochrone{Geometry: [][2]float64{{1, 2}, {3, 4}, {5, 6}}}},
		Geocoder: mockGeo{},
	}
	body := `{"origines":[{"adresse":"a","dist":600},{"adresse":"b","dist":600}]}`
	req := httptest.NewRequest(http.MethodPost, "/etablissements", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.etablissementsInZone(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	for _, want := range []string{`"etablissements":[]`, `"communes":[]`, `"logements_sociaux":[]`, `"zone":null`} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Errorf("body = %s, want %s", rr.Body.String(), want)
		}
	}
}

func TestEtablissementsInZone_OrigineMissingAdresse(t *testing.T) {
	h := &Handlers{Store: mockStore{}, Isochron: mockIso{}, Geocoder: mockGeo{}}
	body := `{"origines":[{"adresse":"a","dist":600},{"adresse":"  ","dist":600}]}`
	req := httptest.NewRequest(http.MethodPost, "/etablissements", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.etablissementsInZone(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rr.Code)
	}
}

func TestEtablissementsInZone_TooManyOrigines(t *testing.T) {
	h := &Handlers{Store: mockStore{}, Isochron: mockIso{}, Geocoder: mockGeo{}}
	o := make([]string, maxOrigines+1)
	for i := range o {
		o[i] = `{"adresse":"a","dist":600}`
	}
	body := `{"origines":[` + strings.Join(o, ",") + `]}`
	req := httptest.NewRequest(http.MethodPost, "/etablissements", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.etablissementsInZone(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rr.Code)
	}
}

func TestIsochrone(t *testing.T) {
	h := &Handlers{Isochron: mockIso{iso: models.Isochrone{Dist: 600, Transp: "driving-car"}}}

	req := httptest.NewRequest(http.MethodGet, "/isochrone?lat=43.6&lon=1.44&dist=600", nil)
	rr := httptest.NewRecorder()
	h.isochrone(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
}

func TestIsochrone_BadParams(t *testing.T) {
	h := &Handlers{Isochron: mockIso{}}

	req := httptest.NewRequest(http.MethodGet, "/isochrone?lat=abc&lon=1.44&dist=600", nil)
	rr := httptest.NewRecorder()
	h.isochrone(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rr.Code)
	}
}

type mockSuggest struct {
	res []geo.BANSuggestion
	err error
}

func (m mockSuggest) Suggest(ctx context.Context, query string, limit int) ([]geo.BANSuggestion, error) {
	return m.res, m.err
}

func TestAddressSuggest(t *testing.T) {
	h := &Handlers{Suggester: mockSuggest{res: []geo.BANSuggestion{{Label: "12 Rue de la Paix 75002 Paris", Longitude: 2.33, Latitude: 48.87}}}}

	req := httptest.NewRequest(http.MethodGet, "/address/suggest?q=12+rue+de+la+pa", nil)
	rr := httptest.NewRecorder()
	h.addressSuggest(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var got []geo.BANSuggestion
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].Label != "12 Rue de la Paix 75002 Paris" {
		t.Errorf("got %+v", got)
	}
}

func TestAddressSuggest_ShortQuery(t *testing.T) {
	h := &Handlers{Suggester: mockSuggest{err: errors.New("should not be called")}}

	req := httptest.NewRequest(http.MethodGet, "/address/suggest?q=ab", nil)
	rr := httptest.NewRecorder()
	h.addressSuggest(rr, req)

	if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != "[]" {
		t.Fatalf("got %d %q, want 200 []", rr.Code, rr.Body.String())
	}
}

func TestAddressSuggest_UpstreamError(t *testing.T) {
	h := &Handlers{Suggester: mockSuggest{err: errors.New("boom")}}

	req := httptest.NewRequest(http.MethodGet, "/address/suggest?q=toulouse", nil)
	rr := httptest.NewRecorder()
	h.addressSuggest(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rr.Code)
	}
}
