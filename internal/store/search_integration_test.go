package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/umaneldi/douceville/internal/models"
)

// lycees ajoute à seedZone des lycées autour de (2.5, 48.5), où se trouve A
// (taux 88,7 %) : D à ~1,3 km (95 %), E à ~13 km (privé, 95 %), F à ~1,5 km
// (sans résultats) et G à ~33 km (100 %).
func lycees(t *testing.T, s *PgStore) {
	t.Helper()
	pool := s.pool
	mustExec(t, pool, `INSERT INTO etablissement ("UAI", nom, code_postal, commune, position, departement, secteur, nature) VALUES
		('D', 'Lycée D', '75001', 'Paris', ST_GeomFromText('POINT(2.51 48.51)'), '75', 'public', 'Lycée'),
		('E', 'Lycée E', '75001', 'Paris', ST_GeomFromText('POINT(2.6 48.6)'), '75', 'privé', 'Lycée'),
		('F', 'Lycée F', '75001', 'Paris', ST_GeomFromText('POINT(2.52 48.5)'), '75', 'public', 'Lycée'),
		('G', 'Lycée G', '75001', 'Paris', ST_GeomFromText('POINT(2.5 48.8)'), '75', 'public', 'Lycée')`)
	mustExec(t, pool, `INSERT INTO resultat (diplome, annee, presents, admis, etablissement_uai) VALUES
		('Bac', 2024, 100, 95, 'D'), ('Bac', 2024, 40, 38, 'E'), ('Bac', 2024, 10, 10, 'G'),
		-- présents nuls ou admis inconnus : ignorés par le taux
		('Bac pro', 2024, 0, 0, 'D'), ('BTS', 2024, 10, NULL, 'D')`)
}

// sourceUAIs liste les UAI des sources, dans l'ordre.
func sourceUAIs(src []models.Source) string {
	var out []string
	for _, s := range src {
		out = append(out, s.UAI)
	}
	return fmt.Sprint(out)
}

func TestEtablissementTauxIntegration(t *testing.T) {
	s, pool := newTestStore(t)
	seedZone(t, pool)
	lycees(t, s)
	ctx := context.Background()

	taux := map[string]float64{}
	rows, err := pool.Query(ctx, `SELECT uai, taux FROM etablissement_taux`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var uai string
		var v float64
		if err := rows.Scan(&uai, &v); err != nil {
			t.Fatal(err)
		}
		taux[uai] = v
	}
	rows.Close()
	if len(taux) != 4 || taux["D"] != 95 || taux["G"] != 100 || taux["A"] < 88.6 || taux["A"] > 88.7 {
		t.Errorf("etablissement_taux = %v, want A≈88.7, D=95, E=95, G=100", taux)
	}
	if _, ok := taux["B"]; ok {
		t.Errorf("B has no results and must be absent: %v", taux)
	}
}

func TestSelectSourceEtablissementsIntegration(t *testing.T) {
	s, pool := newTestStore(t)
	ctx := context.Background()
	centre := [2]float64{2.5, 48.5}
	lycee := models.CritereNature{Nature: "Lycée", TauxMax: 100, Dist: 900, Transp: "driving-car"}

	// Base vide : aucune source, pas d'erreur.
	if src, err := s.SelectSourceEtablissements(ctx, centre, 20000, []models.CritereNature{lycee}, 10); err != nil || len(src) != 0 {
		t.Fatalf("empty base = %v, %v", src, err)
	}
	seedZone(t, pool)
	lycees(t, s)

	// Classement : taux décroissant (D et E à 95 %, départagés par la
	// distance), puis A, puis F sans résultats ; G est hors du rayon.
	src, err := s.SelectSourceEtablissements(ctx, centre, 20000, []models.CritereNature{lycee}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := sourceUAIs(src); got != "[D E A F]" {
		t.Errorf("sources = %s, want [D E A F]", got)
	}
	if d := src[0]; d.Dist != 900 || d.Transp != "driving-car" || d.Taux == nil || *d.Taux != 95 || len(d.Resultats) != 3 {
		t.Errorf("source D = %+v", d)
	}
	if ll, ok := src[0].LonLat(); !ok || ll != [2]float64{2.51, 48.51} {
		t.Errorf("LonLat = %v, %v", ll, ok)
	}
	if src[3].Taux != nil {
		t.Errorf("F taux = %v, want nil", *src[3].Taux)
	}

	// Limite par nature.
	if src, _ := s.SelectSourceEtablissements(ctx, centre, 20000, []models.CritereNature{lycee}, 2); sourceUAIs(src) != "[D E]" {
		t.Errorf("top 2 = %s, want [D E]", sourceUAIs(src))
	}
	// Rayon élargi : G (100 %) passe en tête.
	if src, _ := s.SelectSourceEtablissements(ctx, centre, 40000, []models.CritereNature{lycee}, 1); sourceUAIs(src) != "[G]" {
		t.Errorf("40 km = %s, want [G]", sourceUAIs(src))
	}

	// Critères propres à chaque nature : lycées publics à 90 % et plus (F,
	// sans résultats, est écarté : son taux ne peut pas être vérifié) ;
	// collèges en 15 min à pied, plage 0–100 (B, sans résultats, retenu ; à
	// ~55 km : hors du rayon de 20 km, retenu à 60 km).
	public := lycee
	public.Secteur = []string{"public"}
	public.TauxMin = 90
	college := models.CritereNature{Nature: "Collège", TauxMax: 100, Dist: 600, Transp: "foot-walking"}
	src, err = s.SelectSourceEtablissements(ctx, centre, 20000, []models.CritereNature{public, college}, 10)
	if err != nil || sourceUAIs(src) != "[D]" {
		t.Errorf("public >= 90 %% + collège at 20 km = %s, %v, want [D]", sourceUAIs(src), err)
	}
	src, err = s.SelectSourceEtablissements(ctx, centre, 60000, []models.CritereNature{college, public}, 10)
	if err != nil || sourceUAIs(src) != "[B G D]" {
		t.Fatalf("collège + lycée at 60 km = %s, %v, want [B G D] (critère order, then taux)", sourceUAIs(src), err)
	}
	if src[0].Dist != 600 || src[0].Transp != "foot-walking" || src[1].Transp != "driving-car" {
		t.Errorf("each source must carry its own critère: %+v / %+v", src[0], src[1])
	}

	// Sans critère : rien.
	if src, err := s.SelectSourceEtablissements(ctx, centre, 20000, nil, 10); err != nil || len(src) != 0 {
		t.Errorf("no critère = %v, %v", src, err)
	}
	// Sans plafond (parNature <= 0) : toutes les sources du rayon, dans le
	// même ordre ; au-delà de 20 km, G (100 %) s'ajoute en tête.
	for _, parNature := range []int{0, -1} {
		if src, err := s.SelectSourceEtablissements(ctx, centre, 20000, []models.CritereNature{lycee}, parNature); err != nil || sourceUAIs(src) != "[D E A F]" {
			t.Errorf("parNature %d = %s, %v, want [D E A F]", parNature, sourceUAIs(src), err)
		}
	}
	if src, err := s.SelectSourceEtablissements(ctx, centre, 40000, []models.CritereNature{lycee}, 0); err != nil || sourceUAIs(src) != "[G D E A F]" {
		t.Errorf("no cap at 40 km = %s, %v, want [G D E A F]", sourceUAIs(src), err)
	}
}

func TestFilterZoneIntegration(t *testing.T) {
	s, pool := newTestStore(t)
	seedZone(t, pool)
	ctx := context.Background()
	// Second IRIS de Paris, sans vente (prix de la commune : 3000), et une
	// commune sans aucune donnée.
	mustExec(t, pool, `INSERT INTO commune (code_commune, nom, date_creation) VALUES ('99999', 'Vide', '1943-01-01')`)
	mustExec(t, pool, `INSERT INTO quartier (iris, code_commune, nom_iris, type_iris, contour) VALUES
		('750560002', '75056', 'Est', 'H', ST_Multi(ST_GeomFromText('POLYGON((3 48,4 48,4 49,3 49,3 48))', 4326))),
		('999990001', '99999', 'Vide', 'H', ST_Multi(ST_GeomFromText('POLYGON((4 48,5 48,5 49,4 49,4 48))', 4326)))`)
	const zone = "POLYGON((2 48,5 48,5 49,2 49,2 48))"

	// Le filtre porte sur le score départemental : Paris, seule commune de son
	// département, vaut 50.
	var score float64
	if err := pool.QueryRow(ctx, `SELECT score_departement FROM securite_departement WHERE code_commune = '75056' AND annee = 2024`).Scan(&score); err != nil {
		t.Fatal(err)
	}
	if score != 50 {
		t.Fatalf("score départemental de Paris = %v, want 50", score)
	}
	f := func(v float64) *float64 { return &v }
	area := func(t *testing.T, fz models.FiltresZone) float64 {
		t.Helper()
		wkt, gj, empty, err := s.FilterZone(ctx, zone, fz)
		if err != nil {
			t.Fatal(err)
		}
		if empty {
			if wkt != "" || gj != nil {
				t.Errorf("empty zone: wkt = %q, geojson = %s", wkt, gj)
			}
			return 0
		}
		var a float64
		if err := pool.QueryRow(ctx, `SELECT ST_Area(ST_GeomFromEWKT($1))`, wkt).Scan(&a); err != nil {
			t.Fatal(err)
		}
		return a
	}
	for _, tc := range []struct {
		name string
		f    models.FiltresZone
		want float64
	}{
		{"aucun filtre : tous les IRIS", models.FiltresZone{}, 3},
		// Le second IRIS de Paris prend le prix de la commune ; « Vide » est exclu.
		{"prix min 2500", models.FiltresZone{PrixM2Min: f(2500)}, 2},
		{"prix max 3000", models.FiltresZone{PrixM2Max: f(3000)}, 2},
		{"prix min 3500", models.FiltresZone{PrixM2Min: f(3500)}, 0},
		{"prix max 2500", models.FiltresZone{PrixM2Max: f(2500)}, 0},
		{"sécurité au score de Paris", models.FiltresZone{SecuriteMin: f(score)}, 2},
		{"sécurité au-dessus", models.FiltresZone{SecuriteMin: f(score + 1)}, 0},
		{"fiscalité 26,75", models.FiltresZone{FiscaliteMax: f(26.75)}, 2},
		{"fiscalité 26", models.FiltresZone{FiscaliteMax: f(26)}, 0},
		{"tous les filtres", models.FiltresZone{PrixM2Min: f(1000), PrixM2Max: f(5000), SecuriteMin: f(0), FiscaliteMax: f(50)}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := area(t, tc.f); got < tc.want-0.01 || got > tc.want+0.01 {
				t.Errorf("area = %v, want %v", got, tc.want)
			}
		})
	}

	// La zone est découpée par les quartiers retenus : ici sa moitié ouest.
	wkt, gj, empty, err := s.FilterZone(ctx, "POLYGON((2.5 48,3.5 48,3.5 49,2.5 49,2.5 48))", models.FiltresZone{PrixM2Min: f(0)})
	if err != nil || empty || len(gj) == 0 {
		t.Fatalf("partial zone = %q, %v, %v", wkt, empty, err)
	}
	// Zone hors de tout IRIS : vide.
	if _, _, empty, err := s.FilterZone(ctx, "POLYGON((10 10,11 10,11 11,10 11,10 10))", models.FiltresZone{PrixM2Min: f(0)}); err != nil || !empty {
		t.Errorf("no IRIS = %v, %v", empty, err)
	}
}

func TestIsochroneCacheIntegration(t *testing.T) {
	s, pool := newTestStore(t)
	ctx := context.Background()
	day := 24 * time.Hour

	ka := models.NewIsochroneKey([2]float64{2.123456, 48.654321}, 900, "driving-car")
	kb := models.NewIsochroneKey([2]float64{2.5, 48.5}, 600, "foot-walking")
	kc := models.NewIsochroneKey([2]float64{3, 49}, 600, "foot-walking")
	if ka.LonE5 != 212346 || ka.LatE5 != 4865432 || ka.Dist != 900 {
		t.Fatalf("key = %+v", ka)
	}
	ring := [][2]float64{{1, 2}, {3, 4}, {5, 6}, {1, 2}}
	count := func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM isochrone_cache`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	if got, err := s.GetCachedIsochrones(ctx, nil, day); err != nil || len(got) != 0 {
		t.Fatalf("no key = %v, %v", got, err)
	}
	if err := s.PutCachedIsochrones(ctx, map[models.IsochroneKey][][2]float64{ka: ring, kb: ring}, day, 10); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetCachedIsochrones(ctx, []models.IsochroneKey{ka, kb, kc}, day)
	if err != nil || len(got) != 2 || fmt.Sprint(got[ka]) != fmt.Sprint(ring) {
		t.Fatalf("hits = %v, %v; want ka and kb", got, err)
	}

	// Remplacement idempotent d'une entrée.
	ring2 := [][2]float64{{0, 0}, {1, 0}, {1, 1}, {0, 0}}
	if err := s.PutCachedIsochrones(ctx, map[models.IsochroneKey][][2]float64{ka: ring2}, day, 10); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetCachedIsochrones(ctx, []models.IsochroneKey{ka}, day); fmt.Sprint(got[ka]) != fmt.Sprint(ring2) || count() != 2 {
		t.Errorf("upsert: ring = %v, count = %d", got[ka], count())
	}

	// Entrée trop ancienne : ignorée en lecture, purgée à l'écriture suivante.
	mustExec(t, pool, `UPDATE isochrone_cache SET created_at = now() - interval '2 days' WHERE dist = 600`)
	if got, _ := s.GetCachedIsochrones(ctx, []models.IsochroneKey{ka, kb}, day); len(got) != 1 || got[ka] == nil {
		t.Errorf("expired entry must be a miss: %v", got)
	}
	if err := s.PutCachedIsochrones(ctx, nil, day, 10); err != nil || count() != 1 {
		t.Errorf("purge: count = %d, err = %v; want 1", count(), err)
	}

	// Au-delà de maxEntries : les moins récemment utilisées sont supprimées.
	if err := s.PutCachedIsochrones(ctx, map[models.IsochroneKey][][2]float64{kb: ring}, day, 10); err != nil {
		t.Fatal(err)
	}
	mustExec(t, pool, `UPDATE isochrone_cache SET last_used_at = now() - interval '1 hour' WHERE dist = 900`)
	if err := s.PutCachedIsochrones(ctx, map[models.IsochroneKey][][2]float64{kc: ring}, day, 2); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetCachedIsochrones(ctx, []models.IsochroneKey{ka, kb, kc}, day); len(got) != 2 || got[ka] != nil {
		t.Errorf("LRU eviction: hits = %v, want kb and kc", got)
	}
}

func TestQuartierProximiteIntegration(t *testing.T) {
	s, pool := newTestStore(t)
	ctx := context.Background()

	if q, err := s.QuartierProximite(ctx, "750560001", nil); q != nil || err != nil {
		t.Fatalf("empty base = %+v, %v", q, err)
	}
	seedZone(t, pool)

	lycee := models.CritereNature{Nature: "Lycée", TauxMax: 100}
	college := models.CritereNature{Nature: "Collège", TauxMax: 100}
	q, err := s.QuartierProximite(ctx, "750560001", []models.CritereNature{lycee, college})
	if err != nil || q == nil {
		t.Fatalf("QuartierProximite = %+v, %v", q, err)
	}
	if q.Nom != "Centre" || q.CodeCommune != "75056" || q.Commune != "Paris" {
		t.Errorf("quartier = %+v", q)
	}
	if q.Prix == nil || q.Prix.PrixM2 != 3000 || q.Prix.Ventes != 3 || q.PrixCommune == nil || q.PrixCommune.PrixM2 != 3000 {
		t.Errorf("prix = %+v, commune %+v", q.Prix, q.PrixCommune)
	}
	if q.Securite == nil || q.Securite.Annee != 2024 || q.Securite.NbIndicateurs != 2 {
		t.Fatalf("securite = %+v", q.Securite)
	}
	// Paris : seule commune de son département (score 50), département classé.
	if sd := q.Securite.ScoreDepartement; sd == nil || *sd != 50 {
		t.Errorf("score départemental = %v, want 50", sd)
	}
	if d := q.Securite.Departement; d == nil || d.Code != "75" || d.Nom == nil || *d.Nom != "Paris" || d.NbDepartements != 2 {
		t.Errorf("département = %+v", d)
	}
	if q.Fiscalite == nil || q.Fiscalite.Taux != 26.75 {
		t.Errorf("fiscalite = %+v", q.Fiscalite)
	}
	if len(q.Proches) != 2 {
		t.Fatalf("proches = %+v", q.Proches)
	}
	// Point de départ des distances : intérieur au carré [2, 3] x [48, 49].
	if q.Lon < 2 || q.Lon > 3 || q.Lat < 48 || q.Lat > 49 {
		t.Errorf("point du quartier = (%v, %v), want inside [2, 3] x [48, 49]", q.Lon, q.Lat)
	}
	// Position des établissements (pour l'itinéraire).
	if p := q.Proches[0]; p.Lon == nil || p.Lat == nil || *p.Lon != 2.5 || *p.Lat != 48.5 {
		t.Errorf("position lycée A = %v, %v, want 2.5, 48.5", p.Lon, p.Lat)
	}
	if p := q.Proches[1]; p.Lon == nil || p.Lat == nil || *p.Lon != 2.9 || *p.Lat != 48.9 {
		t.Errorf("position collège B = %v, %v, want 2.9, 48.9", p.Lon, p.Lat)
	}
	// Le point intérieur du carré est proche de son centre (2.5, 48.5), où est A.
	if p := q.Proches[0]; p.Nature != "Lycée" || p.UAI == nil || *p.UAI != "A" || p.DistanceM == nil ||
		*p.DistanceM > 60000 || p.Taux == nil || p.Secteur == nil || *p.Secteur != "public" {
		t.Errorf("lycée = %+v", p)
	}
	if p := q.Proches[1]; p.Nature != "Collège" || p.UAI == nil || *p.UAI != "B" || *p.DistanceM <= 0 {
		t.Errorf("collège = %+v", p)
	}

	// Filtres propres à chaque nature : taux >= 90 écarte A (88,7 %) et C (à
	// La Réunion, sans résultats : taux non vérifiable) ; aucun collège public.
	strict := lycee
	strict.TauxMin = 90
	public := college
	public.Secteur = []string{"public"}
	q, err = s.QuartierProximite(ctx, "750560001", []models.CritereNature{strict, public})
	if err != nil || len(q.Proches) != 2 {
		t.Fatalf("filtered = %+v, %v", q, err)
	}
	if p := q.Proches[0]; p.Nature != "Lycée" || p.UAI != nil || p.DistanceM != nil {
		t.Errorf("lycée >= 90 %% = %+v, want no match", p)
	}
	if p := q.Proches[1]; p.Nature != "Collège" || p.DistanceM != nil || p.UAI != nil || p.Lon != nil || p.Lat != nil {
		t.Errorf("collège public = %+v, want no match", p)
	}

	// B, collège sans résultats : retenu pour une plage de taux complète,
	// écarté dès qu'un taux minimum est exigé (non vérifiable).
	exigeant := college
	exigeant.TauxMin = 50
	q, err = s.QuartierProximite(ctx, "750560001", []models.CritereNature{exigeant})
	if err != nil || len(q.Proches) != 1 {
		t.Fatalf("collège >= 50 %% = %+v, %v", q, err)
	}
	if p := q.Proches[0]; p.UAI != nil || p.DistanceM != nil {
		t.Errorf("collège >= 50 %% = %+v, want no match (B has no results)", p)
	}

	// IRIS sans vente ni sécurité ni fiscalité, sans critère.
	q, err = s.QuartierProximite(ctx, "974110001", nil)
	if err != nil || q == nil || q.Prix == nil || q.Prix.PrixM2 != 1000 || q.Securite == nil || len(q.Proches) != 0 {
		t.Errorf("far IRIS = %+v, %v", q, err)
	}
	if q, err := s.QuartierProximite(ctx, "000000000", []models.CritereNature{lycee}); q != nil || err != nil {
		t.Errorf("unknown IRIS = %+v, %v", q, err)
	}
}
