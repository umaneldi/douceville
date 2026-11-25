package store

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/umaneldi/douceville/internal/models"
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

	uais := func(criteres []models.CritereNature) []string {
		t.Helper()
		etabs, err := s.FindEtablissementsInZone(ctx, testZone, criteres)
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, e := range etabs {
			out = append(out, e.UAI)
			if e.UAI == "A" && (len(e.Resultats) != 3 || e.Taux == nil) {
				t.Errorf("results or taux not loaded: %+v, taux %v", e.Resultats, e.Taux)
			}
		}
		return out
	}
	all := func(nature string, secteur ...string) models.CritereNature {
		return models.CritereNature{Nature: nature, Secteur: secteur, TauxMax: 100}
	}
	// Tri par nature : Collège B, puis Lycée A.
	if got := fmt.Sprint(uais(nil)); got != "[B A]" {
		t.Errorf("no criteria: %v, want [B A]", got)
	}
	if got := fmt.Sprint(uais([]models.CritereNature{all("Lycée")})); got != "[A]" {
		t.Errorf("nature criteria: %v, want [A]", got)
	}
	if got := fmt.Sprint(uais([]models.CritereNature{all("Lycée"), all("Collège", "privé")})); got != "[B A]" {
		t.Errorf("two criteria: %v, want [B A]", got)
	}
	if got := fmt.Sprint(uais([]models.CritereNature{all("Collège", "public")})); got != "[]" {
		t.Errorf("secteur criteria: %v, want []", got)
	}
	// Taux de A : (90 + 99 + 15) / (100 + 110 + 20) = 88,7 %.
	strict := all("Lycée")
	strict.TauxMin = 90
	if got := fmt.Sprint(uais([]models.CritereNature{strict})); got != "[]" {
		t.Errorf("taux >= 90: %v, want []", got)
	}
	strict.TauxMin = 88
	if got := fmt.Sprint(uais([]models.CritereNature{strict})); got != "[A]" {
		t.Errorf("taux >= 88: %v, want [A]", got)
	}
	// B n'a pas de résultats : jamais exclu par le taux.
	college := all("Collège")
	college.TauxMin = 99
	if got := fmt.Sprint(uais([]models.CritereNature{college})); got != "[B]" {
		t.Errorf("no results: %v, want [B]", got)
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

func TestIntersectUnionsIntegration(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	if _, _, _, err := s.IntersectUnions(ctx, nil); err == nil {
		t.Error("no group: expected an error")
	}
	if _, _, _, err := s.IntersectUnions(ctx, [][]string{{testZone}, {}}); err == nil {
		t.Error("empty group: expected an error")
	}

	wkt, gj, empty, err := s.IntersectUnions(ctx, [][]string{{testZone}})
	if err != nil || empty || wkt == "" || !json.Valid(gj) {
		t.Errorf("single polygon = %q, %s, %v, %v", wkt, gj, empty, err)
	}

	area := func(wkt string) float64 {
		t.Helper()
		var a float64
		if err := s.pool.QueryRow(ctx, `SELECT ST_Area(ST_GeomFromText($1))`, wkt).Scan(&a); err != nil {
			t.Fatalf("area of %q: %v", wkt, err)
		}
		return a
	}

	// Deux carrés qui se chevauchent sur lon 2.5–3 : intersection non vide,
	// dont le WKT est réutilisable par les requêtes de zone.
	wkt, _, empty, err = s.IntersectUnions(ctx, [][]string{{testZone}, {"POLYGON((2.5 48,3.5 48,3.5 49,2.5 49,2.5 48))"}})
	if err != nil || empty {
		t.Fatalf("overlap = %v, %v", empty, err)
	}
	if a := area(wkt); a < 0.49 || a > 0.51 {
		t.Errorf("overlap area = %v, want 0.5", a)
	}

	// Union dans un groupe, intersection entre groupes : A = [0,1]×[0,1] et
	// B = [2,3]×[0,1] (disjoints, même critère) ; C = [0.5,2.5]×[0,1] chevauche
	// chacun sur 0.5. L'intersection stricte des trois serait vide.
	a := "POLYGON((0 0,1 0,1 1,0 1,0 0))"
	b := "POLYGON((2 0,3 0,3 1,2 1,2 0))"
	c := "POLYGON((0.5 0,2.5 0,2.5 1,0.5 1,0.5 0))"
	wkt, _, empty, err = s.IntersectUnions(ctx, [][]string{{a, b}, {c}})
	if err != nil || empty {
		t.Fatalf("union then intersection = %v, %v", empty, err)
	}
	if got := area(wkt); got < 0.99 || got > 1.01 {
		t.Errorf("union then intersection area = %v, want 1", got)
	}
	// Union de polygones qui se chevauchent : la surface commune compte une fois.
	wkt, _, empty, err = s.IntersectUnions(ctx, [][]string{{a, c}})
	if err != nil || empty {
		t.Fatalf("overlapping union = %v, %v", empty, err)
	}
	if got := area(wkt); got < 2.49 || got > 2.51 {
		t.Errorf("overlapping union area = %v, want 2.5", got)
	}

	// Carrés disjoints, ou qui ne se touchent que par un bord : zone vide.
	for _, other := range []string{
		"POLYGON((10 10,11 10,11 11,10 11,10 10))",
		"POLYGON((3 48,4 48,4 49,3 49,3 48))",
	} {
		if _, gj, empty, err := s.IntersectUnions(ctx, [][]string{{testZone}, {other}}); err != nil || !empty || gj != nil {
			t.Errorf("%s: empty = %v, geojson = %s, err = %v", other, empty, gj, err)
		}
	}

	// Polygone invalide (papillon) : rendu valide avant l'union.
	if _, _, empty, err := s.IntersectUnions(ctx, [][]string{{"POLYGON((2 48,3 49,3 48,2 49,2 48))"}}); err != nil || empty {
		t.Errorf("bow-tie = %v, %v", empty, err)
	}
}
