package store

import (
	"context"
	"slices"
	"testing"

	"github.com/umaneldi/douceville/internal/models"
)

// annuaire renseigne les attributs de l'annuaire (migration 021) du lycée A
// de seedZone ; le collège B les garde nuls.
func annuaire(t *testing.T, s *PgStore) {
	t.Helper()
	mustExec(t, s.pool, `UPDATE etablissement SET
		education_prioritaire = 'REP+', restauration = true, hebergement = false, ulis = true,
		segpa = false, apprentissage = true, post_bac = true, lycee_des_metiers = false,
		sections = '{Européenne,Sport}', telephone = '0102030405', mail = 'ce.a@ac-paris.fr',
		site_web = 'https://a.example', fiche_onisep = 'https://onisep.example/a'
		WHERE "UAI" = 'A'`)
}

// checkAnnuaireA vérifie les attributs de l'annuaire de A.
func checkAnnuaireA(t *testing.T, e *models.Etablissement) {
	t.Helper()
	str := func(p *string) string {
		if p == nil {
			return "<nil>"
		}
		return *p
	}
	for _, c := range []struct{ name, got, want string }{
		{"education_prioritaire", str(e.EducationPrioritaire), "REP+"},
		{"telephone", str(e.Telephone), "0102030405"},
		{"mail", str(e.Mail), "ce.a@ac-paris.fr"},
		{"site_web", str(e.SiteWeb), "https://a.example"},
		{"fiche_onisep", str(e.FicheOnisep), "https://onisep.example/a"},
	} {
		if c.got != c.want {
			t.Errorf("A %s = %q, want %q", c.name, c.got, c.want)
		}
	}
	for _, c := range []struct {
		name string
		got  *bool
		want bool
	}{
		{"restauration", e.Restauration, true}, {"hebergement", e.Hebergement, false},
		{"ulis", e.ULIS, true}, {"segpa", e.SEGPA, false}, {"apprentissage", e.Apprentissage, true},
		{"post_bac", e.PostBac, true}, {"lycee_des_metiers", e.LyceeDesMetiers, false},
	} {
		if c.got == nil || *c.got != c.want {
			t.Errorf("A %s = %v, want %v", c.name, c.got, c.want)
		}
	}
	if want := []string{"Européenne", "Sport"}; !slices.Equal(e.Sections, want) {
		t.Errorf("A sections = %v, want %v", e.Sections, want)
	}
}

// checkAnnuaireVide vérifie qu'un établissement sans données d'annuaire a des
// attributs nuls et des sections vides (non nil).
func checkAnnuaireVide(t *testing.T, e *models.Etablissement) {
	t.Helper()
	if e.EducationPrioritaire != nil || e.Restauration != nil || e.ULIS != nil || e.SEGPA != nil ||
		e.Telephone != nil || e.Mail != nil || e.SiteWeb != nil || e.FicheOnisep != nil {
		t.Errorf("%s: attributs d'annuaire non nuls : %+v", e.UAI, e)
	}
	if e.Sections == nil || len(e.Sections) != 0 {
		t.Errorf("%s sections = %#v, want vide non nil", e.UAI, e.Sections)
	}
}

func TestAnnuaireIntegration(t *testing.T) {
	s, pool := newTestStore(t)
	seedZone(t, pool)
	annuaire(t, s)
	ctx := context.Background()

	// Rejouée, la migration 021 conserve les valeurs.
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	a, err := s.GetEtablissementByUAI(ctx, "A")
	if err != nil || a == nil {
		t.Fatalf("GetEtablissementByUAI(A) = %+v, %v", a, err)
	}
	checkAnnuaireA(t, a)

	b, err := s.GetEtablissementByUAI(ctx, "B")
	if err != nil || b == nil {
		t.Fatalf("GetEtablissementByUAI(B) = %+v, %v", b, err)
	}
	checkAnnuaireVide(t, b)

	etabs, err := s.FindEtablissementsInZone(ctx, testZone, nil)
	if err != nil {
		t.Fatalf("FindEtablissementsInZone: %v", err)
	}
	seen := 0
	for i := range etabs {
		switch etabs[i].UAI {
		case "A":
			checkAnnuaireA(t, &etabs[i])
			seen++
		case "B":
			checkAnnuaireVide(t, &etabs[i])
			seen++
		}
	}
	if seen != 2 {
		t.Errorf("FindEtablissementsInZone: A et B attendus, got %d sur %d", seen, len(etabs))
	}
}

func TestAnnuaireSourcesIntegration(t *testing.T) {
	s, pool := newTestStore(t)
	seedZone(t, pool)
	annuaire(t, s)
	ctx := context.Background()

	// Les établissements sources (etabColumnsSel) portent aussi les attributs.
	c := models.CritereNature{Nature: "Lycée", TauxMax: 100, Dist: 900, Transp: "driving-car"}
	src, err := s.SelectSourceEtablissements(ctx, [2]float64{2.5, 48.5}, 50000, []models.CritereNature{c}, 10)
	if err != nil {
		t.Fatalf("SelectSourceEtablissements: %v", err)
	}
	for i := range src {
		if src[i].UAI == "A" {
			checkAnnuaireA(t, &src[i].Etablissement)
			return
		}
	}
	t.Errorf("SelectSourceEtablissements: A absent de %d sources", len(src))
}
