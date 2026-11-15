package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ydethe/douceville/internal/geo"
	"github.com/ydethe/douceville/internal/models"
)

// --- Mocks ---

type mockStore struct {
	etab *models.Etablissement
	zone []models.Etablissement
	user *models.DvUser
	err  error
}

func (m mockStore) GetUser(ctx context.Context, id string) (*models.DvUser, error) {
	return m.user, m.err
}
func (m mockStore) GetEtablissementByUAI(ctx context.Context, uai string) (*models.Etablissement, error) {
	return m.etab, m.err
}
func (m mockStore) FindEtablissementsInZone(ctx context.Context, wkt string, nature, secteur []string) ([]models.Etablissement, error) {
	return m.zone, m.err
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
	h := &Handlers{
		Store:    mockStore{zone: []models.Etablissement{{UAI: "A"}, {UAI: "B"}}},
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
