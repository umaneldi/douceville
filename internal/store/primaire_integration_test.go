package store

import (
	"context"
	"fmt"
	"testing"

	"github.com/umaneldi/douceville/internal/models"
)

// ecoles ajoute à seedZone, autour de (2.5, 48.5) : une maternelle M (~1,5 km),
// une élémentaire L (~3 km) et deux écoles nommées « primaire » importées
// avec une autre nature (P, publique, ~0,7 km ; Q, hors contrat, ~5 km),
// puis rejoue les migrations pour que 019 les reclasse en « Primaire ».
func ecoles(t *testing.T, s *PgStore) {
	t.Helper()
	mustExec(t, s.pool, `INSERT INTO etablissement ("UAI", nom, code_postal, commune, position, departement, secteur, nature) VALUES
		('M', 'École maternelle M', '75001', 'Paris', ST_GeomFromText('POINT(2.52 48.5)'), '75', 'public', 'Maternelle'),
		('L', 'École élémentaire L', '75001', 'Paris', ST_GeomFromText('POINT(2.54 48.5)'), '75', 'public', 'Elementaire'),
		('P', 'École primaire P', '75001', 'Paris', ST_GeomFromText('POINT(2.51 48.5)'), '75', 'public', 'Elementaire'),
		('Q', 'Ecole PRIMAIRE privée Q', '75001', 'Paris', ST_GeomFromText('POINT(2.57 48.5)'), '75', 'hors contrat', 'Maternelle')`)
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
}

func TestMigrationPrimaireIntegration(t *testing.T) {
	s, pool := newTestStore(t)
	seedZone(t, pool)
	ecoles(t, s)
	ctx := context.Background()

	natures := func() string {
		t.Helper()
		rows, err := pool.Query(ctx, `SELECT "UAI", nature FROM etablissement WHERE "UAI" IN ('M', 'L', 'P', 'Q', 'A') ORDER BY "UAI"`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var uai, nature string
			if err := rows.Scan(&uai, &nature); err != nil {
				t.Fatal(err)
			}
			out = append(out, uai+"="+nature)
		}
		return fmt.Sprint(out)
	}
	const want = "[A=Lycée L=Elementaire M=Maternelle P=Primaire Q=Primaire]"
	if got := natures(); got != want {
		t.Errorf("natures = %s, want %s", got, want)
	}
	// Idempotente : rejouée, elle ne change plus rien.
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if got := natures(); got != want {
		t.Errorf("natures after second Migrate = %s, want %s", got, want)
	}
}

func TestPrimaireMatchesMaternelleElementaireIntegration(t *testing.T) {
	s, pool := newTestStore(t)
	seedZone(t, pool)
	ecoles(t, s)
	ctx := context.Background()
	centre := [2]float64{2.5, 48.5}

	matern := models.CritereNature{Nature: "Maternelle", TauxMax: 100, Dist: 600, Transp: "foot-walking"}
	elem := models.CritereNature{Nature: "Elementaire", TauxMax: 100, Dist: 900, Transp: "cycling-regular"}

	// Sources : une école primaire est retenue pour chaque critère, avec la
	// nature, le temps et le mode de ce critère (classement par distance).
	src, err := s.SelectSourceEtablissements(ctx, centre, 20000, []models.CritereNature{matern, elem}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := sourceUAIs(src); got != "[P M Q P L Q]" {
		t.Fatalf("sources = %s, want [P M Q P L Q]", got)
	}
	if p := src[0]; p.Nature != models.NaturePrimaire || p.Critere != "Maternelle" || p.Dist != 600 || p.Transp != "foot-walking" {
		t.Errorf("P for maternelle = %+v", p)
	}
	if p := src[3]; p.Nature != models.NaturePrimaire || p.Critere != "Elementaire" || p.Dist != 900 || p.Transp != "cycling-regular" {
		t.Errorf("P for élémentaire = %+v", p)
	}
	if m := src[1]; m.Critere != "Maternelle" || m.Nature != "Maternelle" {
		t.Errorf("M = %+v", m)
	}
	// Le secteur du critère s'applique aussi aux écoles primaires.
	horsContrat := matern
	horsContrat.Secteur = []string{"hors contrat"}
	if src, err := s.SelectSourceEtablissements(ctx, centre, 20000, []models.CritereNature{horsContrat}, 10); err != nil || sourceUAIs(src) != "[Q]" {
		t.Errorf("maternelle hors contrat = %s, %v, want [Q]", sourceUAIs(src), err)
	}
	// Les autres natures ne retiennent pas d'école primaire.
	college := models.CritereNature{Nature: "Collège", TauxMax: 100, Dist: 600, Transp: "foot-walking"}
	if src, err := s.SelectSourceEtablissements(ctx, centre, 100000, []models.CritereNature{college}, 10); err != nil || sourceUAIs(src) != "[B]" {
		t.Errorf("collège = %s, %v, want [B]", sourceUAIs(src), err)
	}

	// Établissements de la zone.
	zone := func(criteres ...models.CritereNature) string {
		t.Helper()
		etabs, err := s.FindEtablissementsInZone(ctx, testZone, criteres)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, e := range etabs {
			out = append(out, e.UAI)
		}
		return fmt.Sprint(out)
	}
	if got := zone(matern); got != "[M P Q]" {
		t.Errorf("zone maternelle = %s, want [M P Q]", got)
	}
	if got := zone(elem); got != "[L P Q]" {
		t.Errorf("zone élémentaire = %s, want [L P Q]", got)
	}
	if got := zone(matern, elem); got != "[L M P Q]" {
		t.Errorf("zone maternelle + élémentaire = %s, want [L M P Q] (sans doublon)", got)
	}
	if got := zone(college); got != "[B]" {
		t.Errorf("zone collège = %s, want [B]", got)
	}

	// Établissement le plus proche du quartier pour chaque critère.
	q, err := s.QuartierProximite(ctx, "750560001", []models.CritereNature{matern, horsContrat, college})
	if err != nil || q == nil || len(q.Proches) != 3 {
		t.Fatalf("QuartierProximite = %+v, %v", q, err)
	}
	if p := q.Proches[0]; p.Nature != "Maternelle" || p.UAI == nil || *p.UAI != "P" {
		t.Errorf("plus proche maternelle = %+v, want P", p)
	}
	if p := q.Proches[1]; p.UAI == nil || *p.UAI != "Q" {
		t.Errorf("plus proche maternelle hors contrat = %+v, want Q", p)
	}
	if p := q.Proches[2]; p.UAI == nil || *p.UAI != "B" {
		t.Errorf("plus proche collège = %+v, want B", p)
	}
}
