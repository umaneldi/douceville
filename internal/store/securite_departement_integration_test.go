package store

import (
	"context"
	"testing"
)

// TestSecuriteDepartementIntegration vérifie la migration 022 : référentiel
// des départements, score départemental (rang centile dans le département),
// classement des départements, et leur lecture par FindCommunesInZone.
func TestSecuriteDepartementIntegration(t *testing.T) {
	s, pool := newTestStore(t)
	ctx := context.Background()

	// Trois communes de Haute-Garonne (31) de plus en plus touchées, et une
	// commune du Tarn (81), seule dans son département et la plus touchée.
	const near = "ST_Multi(ST_GeomFromText('POLYGON((2 48,3 48,3 49,2 49,2 48))', 4326))"
	mustExec(t, pool, `INSERT INTO commune (code_commune, nom, date_creation, contour) VALUES
		('31001', 'Sûre', '1943-01-01', `+near+`),
		('31002', 'Moyenne', '1943-01-01', `+near+`),
		('31003', 'Agitée', '1943-01-01', `+near+`),
		('81004', 'Tarnaise', '1943-01-01', `+near+`)`)
	mustExec(t, pool, `INSERT INTO securite (code_commune, annee, indicateur, unite_de_compte, nombre, taux_pour_mille, est_diffuse) VALUES
		('31001', 2024, 'Vols violents sans arme', 'victime', 1, 1.0, true),
		('31002', 2024, 'Vols violents sans arme', 'victime', 2, 2.0, true),
		('31003', 2024, 'Vols violents sans arme', 'victime', 3, 3.0, true),
		('81004', 2024, 'Vols violents sans arme', 'victime', 10, 10.0, true)`)
	mustExec(t, pool, `REFRESH MATERIALIZED VIEW securite_score`)
	mustExec(t, pool, `REFRESH MATERIALIZED VIEW securite_departement`)

	// Rejouée, la migration ne duplique pas le référentiel.
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	var nbDep int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM departement`).Scan(&nbDep); err != nil || nbDep != 101 {
		t.Fatalf("departement: %d lignes (%v), want 101", nbDep, err)
	}

	communes, err := s.FindCommunesInZone(ctx, testZone)
	if err != nil {
		t.Fatalf("FindCommunesInZone: %v", err)
	}
	// Rang dans le département : (percent_rank + cume_dist) / 2 sur 3 communes
	// -> 17, 58, 100 ; seule dans son département -> 50.
	want := map[string]struct {
		score    int
		dep, nom string
		rang     int
	}{
		"31001": {100, "31", "Haute-Garonne", 1},
		"31002": {58, "31", "Haute-Garonne", 1},
		"31003": {17, "31", "Haute-Garonne", 1},
		"81004": {50, "81", "Tarn", 2},
	}
	if len(communes) != len(want) {
		t.Fatalf("FindCommunesInZone: %d communes, want %d", len(communes), len(want))
	}
	for _, c := range communes {
		w := want[c.CodeCommune]
		if c.ScoreDepartement == nil || *c.ScoreDepartement != w.score {
			t.Errorf("%s score_departement = %v, want %d", c.CodeCommune, c.ScoreDepartement, w.score)
		}
		d := c.Departement
		if d == nil {
			t.Errorf("%s departement = nil", c.CodeCommune)
			continue
		}
		if d.Code != w.dep || d.Nom == nil || *d.Nom != w.nom || d.Rang != w.rang || d.NbDepartements != 2 {
			t.Errorf("%s departement = %+v (nom %v), want %s %s rang %d / 2", c.CodeCommune, d, d.Nom, w.dep, w.nom, w.rang)
		}
	}

	// Le score moyen du département est la moyenne des scores nationaux.
	var moyen, attendu float64
	if err := pool.QueryRow(ctx, `SELECT DISTINCT dep_score_moyen FROM securite_departement WHERE departement = '31'`).Scan(&moyen); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT round(avg(score), 1) FROM securite_score WHERE code_commune LIKE '31%'`).Scan(&attendu); err != nil {
		t.Fatal(err)
	}
	if moyen != attendu {
		t.Errorf("dep_score_moyen(31) = %v, want %v", moyen, attendu)
	}
}

// TestSecuriteDepartementSansDonnees vérifie qu'une commune sans indicateur
// diffusé n'a ni score départemental ni département.
func TestSecuriteDepartementSansDonnees(t *testing.T) {
	s, pool := newTestStore(t)
	ctx := context.Background()
	mustExec(t, pool, `INSERT INTO commune (code_commune, nom, date_creation, contour) VALUES
		('31001', 'Muette', '1943-01-01', ST_Multi(ST_GeomFromText('POLYGON((2 48,3 48,3 49,2 49,2 48))', 4326)))`)
	mustExec(t, pool, `REFRESH MATERIALIZED VIEW securite_score`)
	mustExec(t, pool, `REFRESH MATERIALIZED VIEW securite_departement`)

	communes, err := s.FindCommunesInZone(ctx, testZone)
	if err != nil || len(communes) != 1 {
		t.Fatalf("FindCommunesInZone = %+v, %v", communes, err)
	}
	if c := communes[0]; c.Score != nil || c.ScoreDepartement != nil || c.Departement != nil {
		t.Errorf("commune sans données = %+v", c)
	}
}
