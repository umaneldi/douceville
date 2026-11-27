package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/umaneldi/douceville/internal/models"
)

// estimate appelle POST /api/etablissements/estimation et décode la réponse.
func estimate(t *testing.T, h *Handlers, st mockStore, body string) (int, models.EstimationRecherche) {
	t.Helper()
	rr := callAs(t, h, st, http.MethodPost, "/api/etablissements/estimation", body)
	var est models.EstimationRecherche
	if rr.Code == http.StatusOK {
		if err := json.Unmarshal(rr.Body.Bytes(), &est); err != nil {
			t.Fatalf("decode: %v (%s)", err, rr.Body.String())
		}
	}
	return rr.Code, est
}

func TestEstimation_CountsAndCost(t *testing.T) {
	h, st, _ := billed(t, models.PermsGranted, 100)
	// La source A est déjà en cache : seules B et C sont à calculer.
	a, _ := threeSources()[0].LonLat()
	st.cache = &isoCache{rings: map[models.IsochroneKey][][2]float64{models.NewIsochroneKey(a, 600, "driving-car"): {{1, 2}}}}
	gotCap := -1
	st.parNature = &gotCap
	h.Store = st
	h.SourcesParNature = 0
	h.SearchCostWarningCents = 8

	code, est := estimate(t, h, st, threeOrigins)
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	want := models.EstimationRecherche{Sources: 3, Isochrones: 3, ACalculer: 2, PrixUnitaireCents: 5, CoutCents: 10,
		SoldeCents: 100, AvertissementCents: 8, CriteresVides: []string{}}
	if fmt.Sprint(est) != fmt.Sprint(want) {
		t.Errorf("estimation = %+v, want %+v", est, want)
	}
	if gotCap != 0 {
		t.Errorf("parNature = %d, want 0 (no cap)", gotCap)
	}
	// L'estimation ne débite rien et ne calcule rien.
	if got := st.bill.balances["u1"]; got != 100 {
		t.Errorf("balance = %d, want 100 (estimation is free)", got)
	}
	if st.cache.puts != 0 {
		t.Errorf("cache puts = %d, want 0", st.cache.puts)
	}
}

func TestEstimation_Free(t *testing.T) {
	// Administrateur : gratuit.
	h, st, _ := billed(t, models.PermsAdmin, 0)
	if code, est := estimate(t, h, st, threeOrigins); code != http.StatusOK || !est.Gratuit || est.CoutCents != 0 || est.ACalculer != 3 {
		t.Errorf("admin = %d %+v, want free, 3 to compute", code, est)
	}
	// Facturation désactivée : gratuit, sans lire les tarifs.
	h.Billing = nil
	st.user.Perms = models.PermsGranted
	h.Store = failingStore{mockStore: st, fail: "GetBillingSettings"}
	if code, est := estimate(t, h, st, threeOrigins); code != http.StatusOK || !est.Gratuit || est.PrixUnitaireCents != 0 {
		t.Errorf("billing disabled = %d %+v, want free", code, est)
	}
}

func TestEstimation_CritereVide(t *testing.T) {
	h, st, _ := billed(t, models.PermsGranted, 100)
	st.sources = nil
	h.Store = st
	code, est := estimate(t, h, st, threeOrigins)
	if code != http.StatusOK || est.Sources != 0 || est.ACalculer != 0 || est.CoutCents != 0 ||
		len(est.CriteresVides) != 1 || est.CriteresVides[0] != "Lycée" {
		t.Errorf("no source = %d %+v, want empty critère Lycée, nothing to pay", code, est)
	}
}

func TestEstimation_Errors(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		setup      func(h *Handlers, st *mockStore)
		want       int
	}{
		{"JSON invalide", `{`, nil, http.StatusBadRequest},
		{"sans centre", `{"rayon_km":10,"criteres":[{"nature":"Lycée","dist":600}]}`, nil, http.StatusUnprocessableEntity},
		{"rayon trop grand", `{"centre":"x","rayon_km":99,"criteres":[{"nature":"Lycée","dist":600}]}`, nil, http.StatusUnprocessableEntity},
		{"géocodage en échec", threeOrigins, func(h *Handlers, _ *mockStore) { h.Geocoder = mockGeo{err: errors.New("ban down")} }, http.StatusBadGateway},
		{"sources en échec", threeOrigins, func(h *Handlers, st *mockStore) {
			h.Store = failingStore{mockStore: *st, fail: "SelectSourceEtablissements"}
		}, http.StatusInternalServerError},
		{"tarifs en échec", threeOrigins, func(h *Handlers, st *mockStore) {
			h.Store = failingStore{mockStore: *st, fail: "GetBillingSettings"}
		}, http.StatusInternalServerError},
		{"source sans position", threeOrigins, func(h *Handlers, st *mockStore) {
			st.sources = []models.Source{{Etablissement: models.Etablissement{UAI: "X"}, Critere: "Lycée"}}
			h.Store = *st
		}, http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, st, _ := billed(t, models.PermsGranted, 100)
			if tc.setup != nil {
				tc.setup(h, &st)
			}
			if code, _ := estimate(t, h, st, tc.body); code != tc.want {
				t.Errorf("status = %d, want %d", code, tc.want)
			}
		})
	}
}

func TestEstimation_Rights(t *testing.T) {
	h, st, _ := billed(t, models.PermsGranted, 100)
	h.JWTSecret = testSecret
	// Sans jeton : 403 ; jeton invalide : 401 (comme la recherche).
	for token, want := range map[string]int{"": http.StatusForbidden, "Bearer invalid": http.StatusUnauthorized} {
		req := httptest.NewRequest(http.MethodPost, "/api/etablissements/estimation", strings.NewReader(threeOrigins))
		if token != "" {
			req.Header.Set("Authorization", token)
		}
		rr := httptest.NewRecorder()
		NewRouter(testSecret, h, st).ServeHTTP(rr, req)
		if rr.Code != want {
			t.Errorf("token %q: status = %d, want %d", token, rr.Code, want)
		}
	}
	// Compte non accordé : 403.
	st.user.Perms = models.PermsVerified
	if code, _ := estimate(t, h, st, threeOrigins); code != http.StatusForbidden {
		t.Errorf("verified: status = %d, want 403", code)
	}
}

// Sans plafond, toutes les sources sont gardées et chacune est facturée ; le
// plafond configuré est transmis au store.
func TestSearch_SourcesParNature(t *testing.T) {
	many := make([]models.Source, 25)
	for i := range many {
		many[i] = source(fmt.Sprintf("S%02d", i), "Lycée", 1.40+float64(i)/100, 43.60, 600, "driving-car")
	}
	for _, parNature := range []int{0, 10} {
		h, st, _ := billed(t, models.PermsGranted, 1000)
		st.sources = many
		got := -1
		st.parNature = &got
		h.Store = st
		h.SourcesParNature = parNature
		rr := callAs(t, h, st, http.MethodPost, "/api/etablissements", threeOrigins)
		if rr.Code != http.StatusOK {
			t.Fatalf("parNature %d: status = %d (%s)", parNature, rr.Code, rr.Body.String())
		}
		if got != parNature {
			t.Errorf("store received parNature %d, want %d", got, parNature)
		}
		var resp models.ListeEtablissementsAvecIsochrone
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil || len(resp.Sources) != 25 {
			t.Errorf("sources = %d (%v), want the 25 returned by the store", len(resp.Sources), err)
		}
		if bal := st.bill.balances["u1"]; bal != 1000-25*5 {
			t.Errorf("balance = %d, want %d (25 isochrones × 5)", bal, 1000-25*5)
		}
	}
}

func TestGetBilling_SearchWarning(t *testing.T) {
	h, st, _ := billed(t, models.PermsGranted, 100)
	h.SearchCostWarningCents = 250
	rr := callAs(t, h, st, http.MethodGet, "/api/billing", "")
	var info struct {
		SearchWarningCents int64 `json:"search_warning_cents"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &info); err != nil || info.SearchWarningCents != 250 {
		t.Errorf("search_warning_cents = %d (%v), want 250", info.SearchWarningCents, err)
	}
}
