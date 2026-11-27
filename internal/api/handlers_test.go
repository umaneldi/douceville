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
	// interEmpty est renvoyé par IntersectUnions ; interCalls enregistre les
	// groupes de polygones reçus à chaque appel.
	interEmpty bool
	interCalls *[][][]string
	// emailChanged est renvoyé par VerifyEmail.
	emailChanged bool
	// byID est renvoyé par GetUserRecordByID.
	byID *models.UserRecord
	// setPassword reçoit le hash passé à SetPassword ; emailChange reçoit la
	// nouvelle adresse passée à CreateEmailChange (si non nil).
	setPassword *string
	emailChange *string
	// oidcUser et oidcErr sont renvoyés par FindOrCreateOIDCUser.
	oidcUser *models.DvUser
	oidcErr  error
	// users est renvoyé par ListUsers ; permsPrev est le niveau précédent
	// renvoyé par SetUserPerms. setPermsTo et deleted reçoivent le niveau passé
	// à SetUserPerms et l'identifiant passé à DeleteUser (si non nil).
	users      []models.DvUser
	permsPrev  models.Perms
	setPermsTo *models.Perms
	deleted    *string
	// bill porte l'état de facturation simulé (nil : tarifs nuls, aucun mouvement).
	bill *fakeBill
	// sources est renvoyé par SelectSourceEtablissements.
	sources []models.Source
	// cache simule le cache d'isochrones (nil : toujours vide, écritures ignorées).
	cache *isoCache
	// filterEmpty est renvoyé par FilterZone ; filterCalls compte ses appels.
	filterEmpty bool
	filterCalls *int
	// proximite est renvoyé par QuartierProximite.
	proximite *models.QuartierProximite
	// criteres reçoit les critères passés à FindEtablissementsInZone ou
	// QuartierProximite (si non nil).
	criteres *[]models.CritereNature
}

// isoCache est un cache d'isochrones en mémoire ; getErr et putErr font
// échouer la lecture et l'écriture.
type isoCache struct {
	rings  map[models.IsochroneKey][][2]float64
	puts   int
	getErr error
	putErr error
}

func (m mockStore) SelectSourceEtablissements(ctx context.Context, centre [2]float64, rayonM float64, criteres []models.CritereNature, parNature int) ([]models.Source, error) {
	return m.sources, m.err
}
func (m mockStore) GetCachedIsochrones(ctx context.Context, keys []models.IsochroneKey, maxAge time.Duration) (map[models.IsochroneKey][][2]float64, error) {
	out := map[models.IsochroneKey][][2]float64{}
	if m.cache == nil {
		return out, m.err
	}
	if m.cache.getErr != nil {
		return nil, m.cache.getErr
	}
	for _, k := range keys {
		if r, ok := m.cache.rings[k]; ok {
			out[k] = r
		}
	}
	return out, m.err
}
func (m mockStore) PutCachedIsochrones(ctx context.Context, entries map[models.IsochroneKey][][2]float64, maxAge time.Duration, maxEntries int) error {
	if m.cache == nil {
		return m.err
	}
	if m.cache.putErr != nil {
		return m.cache.putErr
	}
	m.cache.puts++
	if m.cache.rings == nil {
		m.cache.rings = map[models.IsochroneKey][][2]float64{}
	}
	for k, r := range entries {
		m.cache.rings[k] = r
	}
	return m.err
}
func (m mockStore) FilterZone(ctx context.Context, wkt string, f models.FiltresZone) (string, json.RawMessage, bool, error) {
	if m.filterCalls != nil {
		*m.filterCalls++
	}
	if m.filterEmpty {
		return "", nil, true, m.err
	}
	return "POLYGON((1 2,3 4,5 6,1 2))", json.RawMessage(`{"type":"Polygon","coordinates":[]}`), false, m.err
}
func (m mockStore) QuartierProximite(ctx context.Context, iris string, criteres []models.CritereNature) (*models.QuartierProximite, error) {
	if m.criteres != nil {
		*m.criteres = criteres
	}
	return m.proximite, m.err
}

// knownUser renvoie le compte d'identifiant id parmi m.users.
func (m mockStore) knownUser(id string) *models.DvUser {
	for i := range m.users {
		if m.users[i].ID == id {
			u := m.users[i]
			return &u
		}
	}
	return nil
}
func (m mockStore) ListUsers(ctx context.Context) ([]models.DvUser, error) {
	return m.users, m.err
}
func (m mockStore) SetUserPerms(ctx context.Context, id string, perms models.Perms) (*models.DvUser, models.Perms, error) {
	if m.err != nil {
		return nil, "", m.err
	}
	u := m.knownUser(id)
	if u == nil {
		return nil, "", nil
	}
	if m.setPermsTo != nil {
		*m.setPermsTo = perms
	}
	u.Perms = perms
	return u, m.permsPrev, nil
}
func (m mockStore) DeleteUser(ctx context.Context, id string) (bool, error) {
	if m.err != nil {
		return false, m.err
	}
	if m.knownUser(id) == nil {
		return false, nil
	}
	if m.deleted != nil {
		*m.deleted = id
	}
	return true, nil
}

func (m mockStore) FindOrCreateOIDCUser(ctx context.Context, id models.OIDCIdentity, defaultPerms models.Perms) (*models.DvUser, error) {
	return m.oidcUser, m.oidcErr
}

func (m mockStore) IntersectUnions(ctx context.Context, groups [][]string) (string, json.RawMessage, bool, error) {
	if m.interCalls != nil {
		*m.interCalls = append(*m.interCalls, groups)
	}
	if m.interEmpty {
		return "", nil, true, m.err
	}
	return "MULTIPOLYGON(((1 2,3 4,5 6,1 2)))", json.RawMessage(`{"type":"MultiPolygon","coordinates":[]}`), false, m.err
}

func (m mockStore) CreateEmailVerification(ctx context.Context, userID, tokenHash string, expiresAt time.Time) error {
	return m.err
}
func (m mockStore) PeekEmailVerification(ctx context.Context, tokenHash string) (string, string, bool, error) {
	if m.verified == nil || m.err != nil {
		return "", "", false, m.err
	}
	email := ""
	if m.verified.Email != nil {
		email = *m.verified.Email
	}
	return m.verified.ID, email, true, nil
}
func (m mockStore) VerifyEmail(ctx context.Context, tokenHash string) (*models.DvUser, bool, error) {
	return m.verified, m.emailChanged, m.err
}
func (m mockStore) GetUserRecordByID(ctx context.Context, id string) (*models.UserRecord, error) {
	return m.byID, m.err
}
func (m mockStore) UpdateProfile(ctx context.Context, id string, p models.ProfileUpdate) (*models.DvUser, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &models.DvUser{ID: id, FirstName: p.FirstName, LastName: p.LastName, AvatarURL: p.AvatarURL}, nil
}
func (m mockStore) SetPassword(ctx context.Context, id, passwordHash string) error {
	if m.setPassword != nil {
		*m.setPassword = passwordHash
	}
	return m.err
}
func (m mockStore) CreateEmailChange(ctx context.Context, userID, newEmail, tokenHash string, expiresAt time.Time) error {
	if m.emailChange != nil {
		*m.emailChange = newEmail
	}
	return m.err
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
func (m mockStore) FindEtablissementsInZone(ctx context.Context, wkt string, criteres []models.CritereNature) ([]models.Etablissement, error) {
	if m.criteres != nil {
		*m.criteres = criteres
	}
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

// mockIso simule OpenRouteService. CalcIsochrones renvoie, pour chaque
// position, un triangle qui commence par elle ; calls (si non nil) reçoit
// chaque appel.
type mockIso struct {
	iso   models.Isochrone
	err   error
	calls *[]isoCall
}

// isoCall est un appel à CalcIsochrones.
type isoCall struct {
	lonlats [][2]float64
	dist    float64
	transp  string
}

func (m mockIso) CalcIsochrone(ctx context.Context, lonlat [2]float64, dist, interval float64, transp string) (models.Isochrone, error) {
	return m.iso, m.err
}

func (m mockIso) CalcIsochrones(ctx context.Context, lonlats [][2]float64, dist float64, transp string) ([]models.Isochrone, error) {
	if m.calls != nil {
		*m.calls = append(*m.calls, isoCall{lonlats, dist, transp})
	}
	if m.err != nil {
		return nil, m.err
	}
	out := make([]models.Isochrone, len(lonlats))
	for i, p := range lonlats {
		out[i] = models.Isochrone{LonLat: p, Dist: dist, Transp: transp,
			Geometry: [][2]float64{p, {p[0] + 0.1, p[1]}, {p[0], p[1] + 0.1}, p}}
	}
	return out, nil
}

// mockRoute simule les itinéraires OpenRouteService : renvoie un segment
// direct de from à to ; calls (si non nil) reçoit chaque appel.
type mockRoute struct {
	err   error
	calls *[]routeCall
}

// routeCall est un appel à CalcItineraire.
type routeCall struct {
	from, to [2]float64
	transp   string
}

func (m mockRoute) CalcItineraire(ctx context.Context, from, to [2]float64, transp string) (models.Itineraire, error) {
	if m.calls != nil {
		*m.calls = append(*m.calls, routeCall{from, to, transp})
	}
	if m.err != nil {
		return models.Itineraire{}, m.err
	}
	return models.Itineraire{Geometry: [][2]float64{from, to}, DistanceM: 1500, DureeS: 300}, nil
}

type mockGeo struct {
	res geo.BANResult
	err error
}

func (m mockGeo) Geocode(ctx context.Context, query string) (geo.BANResult, error) {
	return m.res, m.err
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

// Les attributs de l'annuaire sont sérialisés quand ils sont renseignés et
// omis quand ils sont nuls (via le routeur, en tant qu'utilisateur autorisé).
func TestReadEtablissement_Annuaire(t *testing.T) {
	rep, tel, vrai := "REP+", "0102030405", true
	plein := &models.Etablissement{UAI: "A", Nom: "Lycée A", EducationPrioritaire: &rep,
		Restauration: &vrai, Sections: []string{"Européenne"}, Telephone: &tel}
	for _, tc := range []struct {
		name string
		etab *models.Etablissement
		want map[string]any
	}{
		{"renseignés", plein, map[string]any{
			"education_prioritaire": "REP+", "restauration": true,
			"sections": []any{"Européenne"}, "telephone": "0102030405"}},
		{"nuls", &models.Etablissement{UAI: "B", Nom: "Collège B"}, map[string]any{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := alice(t, true) // niveau granted
			st.etab = tc.etab
			rr := call(t, &Handlers{Store: st}, http.MethodGet, "/api/etablissement/"+tc.etab.UAI, "")
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body)
			}
			var got map[string]any
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			for _, k := range []string{"education_prioritaire", "restauration", "hebergement", "ulis",
				"segpa", "apprentissage", "post_bac", "lycee_des_metiers", "sections",
				"telephone", "mail", "site_web", "fiche_onisep"} {
				want, ok := tc.want[k]
				g, present := got[k]
				if present != ok || (ok && fmt.Sprint(g) != fmt.Sprint(want)) {
					t.Errorf("%s = %v (présent %v), want %v (présent %v)", k, g, present, want, ok)
				}
			}
		})
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
