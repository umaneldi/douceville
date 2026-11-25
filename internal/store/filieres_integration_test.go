package store

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/umaneldi/douceville/internal/models"
)

// filieres renseigne les filières des lycées de seedZone et lycees : A
// général et technologique, D professionnel, E général, professionnel et
// agricole, G général ; F garde le tableau vide par défaut.
func filieres(t *testing.T, s *PgStore) {
	t.Helper()
	mustExec(t, s.pool, `UPDATE etablissement SET filieres = v.f FROM (VALUES
		('A', '{Général,Technologique}'::text[]),
		('D', '{Professionnel}'),
		('E', '{Général,Professionnel,Agricole}'),
		('G', '{Général}')) AS v(uai, f)
		WHERE "UAI" = v.uai`)
}

func TestMigrationFilieresIntegration(t *testing.T) {
	s, pool := newTestStore(t)
	seedZone(t, pool)
	lycees(t, s)
	filieres(t, s)
	ctx := context.Background()

	// Rejouée, la migration 020 ne touche pas aux valeurs ; défaut : tableau vide.
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	e, err := s.GetEtablissementByUAI(ctx, "E")
	if err != nil || e == nil {
		t.Fatalf("GetEtablissementByUAI(E) = %+v, %v", e, err)
	}
	if want := []string{"Général", "Professionnel", "Agricole"}; !slices.Equal(e.Filieres, want) {
		t.Errorf("E filieres = %v, want %v", e.Filieres, want)
	}
	for _, uai := range []string{"F", "B"} {
		e, err := s.GetEtablissementByUAI(ctx, uai)
		if err != nil || e == nil || e.Filieres == nil || len(e.Filieres) != 0 {
			t.Errorf("%s filieres = %#v, %v; want empty, non-nil", uai, e, err)
		}
	}
}

func TestFiliereCritereIntegration(t *testing.T) {
	s, pool := newTestStore(t)
	seedZone(t, pool)
	lycees(t, s)
	filieres(t, s)
	ctx := context.Background()
	centre := [2]float64{2.5, 48.5}
	lycee := func(f ...string) models.CritereNature {
		return models.CritereNature{Nature: "Lycée", Filiere: f, TauxMax: 100, Dist: 900, Transp: "driving-car"}
	}

	// Sources (rayon 20 km : D E A F sans filtre). Au moins une filière en
	// commun ; un lycée sans filière n'est retenu que sans filtre.
	for _, tc := range []struct {
		filieres []string
		want     string
	}{
		{nil, "[D E A F]"},
		{[]string{"Professionnel"}, "[D E]"},
		{[]string{"Général"}, "[E A]"},
		{[]string{"Technologique", "Agricole"}, "[E A]"},
	} {
		src, err := s.SelectSourceEtablissements(ctx, centre, 20000, []models.CritereNature{lycee(tc.filieres...)}, 10)
		if err != nil || sourceUAIs(src) != tc.want {
			t.Errorf("sources %v = %s, %v, want %s", tc.filieres, sourceUAIs(src), err, tc.want)
		}
	}
	src, err := s.SelectSourceEtablissements(ctx, centre, 20000, []models.CritereNature{lycee("Professionnel")}, 1)
	if err != nil || len(src) != 1 || !slices.Equal(src[0].Filieres, []string{"Professionnel"}) {
		t.Errorf("source D = %+v, %v; want filieres [Professionnel]", src, err)
	}

	// Établissements de la zone (lycées A D E F G).
	zone := func(c models.CritereNature) string {
		t.Helper()
		etabs, err := s.FindEtablissementsInZone(ctx, testZone, []models.CritereNature{c})
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, e := range etabs {
			out = append(out, e.UAI+fmt.Sprint(e.Filieres))
		}
		return fmt.Sprint(out)
	}
	if got, want := zone(lycee()), "[A[Général Technologique] D[Professionnel] E[Général Professionnel Agricole] F[] G[Général]]"; got != want {
		t.Errorf("zone = %s, want %s", got, want)
	}
	if got, want := zone(lycee("Général")), "[A[Général Technologique] E[Général Professionnel Agricole] G[Général]]"; got != want {
		t.Errorf("zone général = %s, want %s", got, want)
	}

	// Lycée le plus proche d'un quartier : seul E est agricole.
	q, err := s.QuartierProximite(ctx, "750560001", []models.CritereNature{lycee("Agricole")})
	if err != nil || q == nil || len(q.Proches) != 1 || q.Proches[0].UAI == nil || *q.Proches[0].UAI != "E" {
		t.Fatalf("proche agricole = %+v, %v; want E", q, err)
	}
}
