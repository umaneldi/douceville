package store

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Zone de recherche des tests : le carré lon 2–3, lat 48–49.
const testZone = "POLYGON((2 48,3 48,3 49,2 49,2 48))"

// seedZone charge un jeu de données minimal : une commune (75056) couvrant
// testZone et une commune lointaine (97411), chacune avec un IRIS, des ventes,
// des indicateurs de sécurité, de la fiscalité, des établissements et des
// logements sociaux.
func seedZone(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	const near = "ST_Multi(ST_GeomFromText('POLYGON((2 48,3 48,3 49,2 49,2 48))', 4326))"
	const far = "ST_Multi(ST_GeomFromText('POLYGON((55 -21,56 -21,56 -20,55 -20,55 -21))', 4326))"
	for _, q := range []string{
		`INSERT INTO commune (code_commune, nom, date_creation, contour) VALUES
			('75056', 'Paris', '1943-01-01', ` + near + `),
			('97411', 'Saint-Denis', '1943-01-01', ` + far + `),
			('00000', 'Sans contour', '1943-01-01', NULL)`,
		`INSERT INTO quartier (iris, code_commune, nom_iris, type_iris, contour) VALUES
			('750560001', '75056', 'Centre', 'H', ` + near + `),
			('974110001', '97411', 'Loin', 'H', ` + far + `)`,
		// Prix au m² : 2000, 3000, 4000 -> médiane 3000 ; IRIS lointain : 1000.
		`INSERT INTO immobilier (iris, date_mutation, nature_mutation, valeur_fonciere, id_parcelle, surface_reelle_bati) VALUES
			('750560001', '2023-02-01', 'Vente', 200000, 'p1', 100),
			('750560001', '2024-06-01', 'Vente', 300000, 'p2', 100),
			('750560001', '2024-12-31', 'Vente', 400000, 'p3', 100),
			('974110001', '2022-01-15', 'Vente', 100000, 'p4', 100)`,
		`INSERT INTO securite (code_commune, annee, indicateur, unite_de_compte, nombre, taux_pour_mille, est_diffuse) VALUES
			('75056', 2023, 'Vols violents sans arme', 'victime', 10, 5.0, true),
			('75056', 2024, 'Vols violents sans arme', 'victime', 10, 5.0, true),
			('75056', 2024, 'Cambriolages de logement', 'logement', 20, 2.0, true),
			('97411', 2024, 'Vols violents sans arme', 'victime', 1, 0.5, true),
			('97411', 2024, 'Cambriolages de logement', 'logement', 1, 0.1, true)`,
		`REFRESH MATERIALIZED VIEW securite_score`,
		`INSERT INTO fiscalite (code_commune, year, taux_global_tfb, taux_plein_teom) VALUES
			('75056', 2023, 10.0, 5.0),
			('75056', 2024, 20.5, 6.25),
			('97411', 2024, 30.0, NULL)`,
		`INSERT INTO etablissement ("UAI", nom, code_postal, commune, position, departement, secteur, nature, code_commune) VALUES
			('A', 'Lycée A', '75001', 'Paris', ST_GeomFromText('POINT(2.5 48.5)'), '75', 'public', 'Lycée', '75056'),
			('B', 'Collège B', '75002', 'Paris', ST_GeomFromText('POINT(2.9 48.9)'), '75', 'privé', 'Collège', '75056'),
			('C', 'Lycée C', '97400', 'Saint-Denis', ST_GeomFromText('POINT(55.5 -20.5)'), '974', 'public', 'Lycée', '97411')`,
		`INSERT INTO resultat (diplome, annee, presents, admis, etablissement_uai) VALUES
			('Bac', 2023, 100, 90, 'A'),
			('Bac', 2024, 110, 99, 'A'),
			('Bac pro', 2024, 20, 15, 'A')`,
		// Deux logements identiques et un différent au même bâtiment, un autre
		// bâtiment dans la zone, un bâtiment hors zone, un sans position.
		`INSERT INTO logement_social (iris, type_construction, nb_pieces, surface, annee_construction, position) VALUES
			('750560001', 'C', 3, 60, 1970, ST_GeomFromText('POINT(2.4 48.4)')),
			('750560001', 'C', 3, 60, 1970, ST_GeomFromText('POINT(2.4 48.4)')),
			('750560001', 'C', 2, 45, 1970, ST_GeomFromText('POINT(2.4 48.4)')),
			('750560001', 'I', 5, 100, 2010, ST_GeomFromText('POINT(2.2 48.2)')),
			('974110001', 'C', 1, 30, 2000, ST_GeomFromText('POINT(55.5 -20.5)')),
			('750560001', 'C', 1, 30, 2000, NULL)`,
	} {
		mustExec(t, pool, q)
	}
}

func TestEtablissementsIntegration(t *testing.T) {
	s, pool := newTestStore(t)
	seedZone(t, pool)
	ctx := context.Background()

	e, err := s.GetEtablissementByUAI(ctx, "A")
	if err != nil || e == nil {
		t.Fatalf("GetEtablissementByUAI = %+v, %v", e, err)
	}
	if e.Nom != "Lycée A" || len(e.Resultats) != 3 || e.Resultats[0].Annee != 2024 || e.Resultats[0].Diplome != "Bac" {
		t.Errorf("etablissement = %+v", e)
	}
	var pos struct {
		Type        string     `json:"type"`
		Coordinates [2]float64 `json:"coordinates"`
	}
	if err := json.Unmarshal(e.Position, &pos); err != nil || pos.Type != "Point" || pos.Coordinates != [2]float64{2.5, 48.5} {
		t.Errorf("position = %s (%v)", e.Position, err)
	}
	if e, err := s.GetEtablissementByUAI(ctx, "ZZZ"); e != nil || err != nil {
		t.Errorf("unknown UAI = %+v, %v", e, err)
	}

	uais := func(nature, secteur []string) []string {
		t.Helper()
		etabs, err := s.FindEtablissementsInZone(ctx, testZone, nature, secteur)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, e := range etabs {
			out = append(out, e.UAI)
			if e.UAI == "A" && len(e.Resultats) != 3 {
				t.Errorf("results not loaded: %+v", e.Resultats)
			}
		}
		return out
	}
	if got := uais(nil, nil); len(got) != 2 {
		t.Errorf("no filter: %v, want [A B]", got)
	}
	if got := uais([]string{"Lycée"}, nil); len(got) != 1 || got[0] != "A" {
		t.Errorf("nature filter: %v", got)
	}
	if got := uais(nil, []string{"privé"}); len(got) != 1 || got[0] != "B" {
		t.Errorf("secteur filter: %v", got)
	}
	if got := uais([]string{"Collège"}, []string{"public"}); len(got) != 0 {
		t.Errorf("combined filters: %v", got)
	}
}

func TestCommunesInZoneIntegration(t *testing.T) {
	s, pool := newTestStore(t)
	ctx := context.Background()

	if cs, err := s.FindCommunesInZone(ctx, testZone); err != nil || len(cs) != 0 {
		t.Fatalf("empty base = %+v, %v", cs, err)
	}
	seedZone(t, pool)

	cs, err := s.FindCommunesInZone(ctx, testZone)
	if err != nil || len(cs) != 1 {
		t.Fatalf("FindCommunesInZone = %+v, %v", cs, err)
	}
	c := cs[0]
	if c.CodeCommune != "75056" || c.Nom != "Paris" || c.Annee == nil || *c.Annee != 2024 ||
		c.NbIndicateurs == nil || *c.NbIndicateurs != 2 || c.Score == nil {
		t.Errorf("commune = %+v", c)
	}
	// Taux plus élevés que la commune lointaine sur les deux indicateurs :
	// Paris est la moins sûre des deux.
	if *c.Score >= 100 || *c.Score < 0 {
		t.Errorf("score = %d", *c.Score)
	}
	if f := c.Fiscalite; f == nil || f.Annee != 2024 || f.TauxTFB != 20.5 || f.TauxTEOM == nil || *f.TauxTEOM != 6.25 || f.Taux != 26.75 {
		t.Errorf("fiscalite = %+v", c.Fiscalite)
	}
	if len(c.Contour) == 0 || !json.Valid(c.Contour) {
		t.Errorf("contour = %s", c.Contour)
	}

	// Commune sans TEOM : taux = TFB seule.
	far := "POLYGON((55 -21,56 -21,56 -20,55 -20,55 -21))"
	cs, err = s.FindCommunesInZone(ctx, far)
	if err != nil || len(cs) != 1 || cs[0].Fiscalite == nil || cs[0].Fiscalite.TauxTEOM != nil || cs[0].Fiscalite.Taux != 30 {
		t.Errorf("far commune = %+v, %v", cs, err)
	}
	if *cs[0].Score <= *c.Score {
		t.Errorf("safer commune should score higher: %d <= %d", *cs[0].Score, *c.Score)
	}
}

func TestImmobilierInZoneIntegration(t *testing.T) {
	s, pool := newTestStore(t)
	ctx := context.Background()

	z, err := s.FindImmobilierInZone(ctx, testZone, []string{"75056"})
	if err != nil || z.DateMin != nil || len(z.Quartiers) != 0 || len(z.Communes) != 0 {
		t.Fatalf("empty table = %+v, %v", z, err)
	}
	seedZone(t, pool)

	z, err = s.FindImmobilierInZone(ctx, testZone, []string{"75056"})
	if err != nil {
		t.Fatal(err)
	}
	if z.DateMin == nil || *z.DateMin != "2022-01-15" || *z.DateMax != "2024-12-31" {
		t.Errorf("dates = %v, %v", z.DateMin, z.DateMax)
	}
	if len(z.Quartiers) != 1 {
		t.Fatalf("quartiers = %+v", z.Quartiers)
	}
	q := z.Quartiers[0]
	if q.Iris != "750560001" || q.CodeCommune != "75056" || q.Nom != "Centre" || q.Prix == nil ||
		q.Prix.PrixM2 != 3000 || q.Prix.Ventes != 3 || !json.Valid(q.Contour) {
		t.Errorf("quartier = %+v (prix %+v)", q, q.Prix)
	}
	if len(z.Communes) != 1 || z.Communes[0].CodeCommune != "75056" || z.Communes[0].Prix.PrixM2 != 3000 {
		t.Errorf("communes = %+v", z.Communes)
	}

	// Sans code commune : seulement les quartiers.
	if z, err := s.FindImmobilierInZone(ctx, testZone, nil); err != nil || len(z.Communes) != 0 || len(z.Quartiers) != 1 {
		t.Errorf("no communes = %+v, %v", z, err)
	}
}

func TestLogementsSociauxInZoneIntegration(t *testing.T) {
	s, pool := newTestStore(t)
	seedZone(t, pool)
	ctx := context.Background()

	ps, err := s.FindLogementsSociauxInZone(ctx, testZone)
	if err != nil || len(ps) != 2 {
		t.Fatalf("FindLogementsSociauxInZone = %+v, %v", ps, err)
	}
	// Tri par position : (2.2, 48.2) puis (2.4, 48.4).
	if ps[0].Lon != 2.2 || ps[0].NbLogements != 1 || len(ps[0].Logements) != 1 {
		t.Errorf("first position = %+v", ps[0])
	}
	p := ps[1]
	if p.Lon != 2.4 || p.Lat != 48.4 || p.NbLogements != 3 || len(p.Logements) != 2 {
		t.Fatalf("grouped position = %+v", p)
	}
	// Groupes triés par nombre de pièces : 2 pièces (×1), puis 3 pièces (×2).
	if *p.Logements[0].NbPieces != 2 || p.Logements[0].Nb != 1 || *p.Logements[1].NbPieces != 3 || p.Logements[1].Nb != 2 {
		t.Errorf("groups = %+v", p.Logements)
	}
}

func TestIntersectZonesIntegration(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	if _, _, _, err := s.IntersectZones(ctx, nil); err == nil {
		t.Error("no polygon: expected an error")
	}

	wkt, gj, empty, err := s.IntersectZones(ctx, []string{testZone})
	if err != nil || empty || wkt == "" || !json.Valid(gj) {
		t.Errorf("single polygon = %q, %s, %v, %v", wkt, gj, empty, err)
	}

	// Deux carrés qui se chevauchent sur lon 2.5–3 : intersection non vide,
	// dont le WKT est réutilisable par les requêtes de zone.
	wkt, _, empty, err = s.IntersectZones(ctx, []string{testZone, "POLYGON((2.5 48,3.5 48,3.5 49,2.5 49,2.5 48))"})
	if err != nil || empty {
		t.Fatalf("overlap = %v, %v", empty, err)
	}
	var area float64
	if err := s.pool.QueryRow(ctx, `SELECT ST_Area(ST_GeomFromText($1))`, wkt).Scan(&area); err != nil || area < 0.49 || area > 0.51 {
		t.Errorf("overlap area = %v (%v), want 0.5", area, err)
	}

	// Carrés disjoints, ou qui ne se touchent que par un bord : zone vide.
	for _, other := range []string{
		"POLYGON((10 10,11 10,11 11,10 11,10 10))",
		"POLYGON((3 48,4 48,4 49,3 49,3 48))",
	} {
		if _, gj, empty, err := s.IntersectZones(ctx, []string{testZone, other}); err != nil || !empty || gj != nil {
			t.Errorf("%s: empty = %v, geojson = %s, err = %v", other, empty, gj, err)
		}
	}

	// Polygone invalide (papillon) : rendu valide avant l'intersection.
	if _, _, empty, err := s.IntersectZones(ctx, []string{"POLYGON((2 48,3 49,3 48,2 49,2 48))"}); err != nil || empty {
		t.Errorf("bow-tie = %v, %v", empty, err)
	}
}

func TestPointLevelsIntegration(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	inner := "POLYGON((2.4 48.4,2.6 48.4,2.6 48.6,2.4 48.6,2.4 48.4))"
	got, err := s.PointLevels(ctx, [][2]float64{{2.5, 48.5}, {2.9, 48.9}, {10, 10}}, []string{inner, testZone})
	if err != nil || len(got) != 3 || got[0] != 0 || got[1] != 1 || got[2] != -1 {
		t.Errorf("PointLevels = %v, %v; want [0 1 -1]", got, err)
	}

	if got, err := s.PointLevels(ctx, [][2]float64{{2.5, 48.5}}, nil); err != nil || len(got) != 1 || got[0] != -1 {
		t.Errorf("no level = %v, %v", got, err)
	}
	if got, err := s.PointLevels(ctx, nil, []string{testZone}); err != nil || len(got) != 0 {
		t.Errorf("no point = %v, %v", got, err)
	}
}
