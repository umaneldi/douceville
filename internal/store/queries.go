package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/ydethe/douceville/internal/models"
)

// colonnes sélectionnées pour un établissement. position est émise en GeoJSON.
const etabColumns = `id, "UAI", nom, adresse, lieu_dit, code_postal, commune,
	ST_AsGeoJSON(position) AS position, departement, academie, secteur, ouverture, nature`

func scanEtablissement(row pgx.Row) (*models.Etablissement, error) {
	var e models.Etablissement
	err := row.Scan(
		&e.ID, &e.UAI, &e.Nom, &e.Adresse, &e.LieuDit, &e.CodePostal, &e.Commune,
		&e.Position, &e.Departement, &e.Academie, &e.Secteur, &e.Ouverture, &e.Nature,
	)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// GetUser implémente Store.GetUser.
func (s *PgStore) GetUser(ctx context.Context, id string) (*models.DvUser, error) {
	const q = `SELECT id, email, first_name, last_name, avatar_url, permissions
		FROM utilisateur WHERE id = $1`

	var u models.DvUser
	err := s.pool.QueryRow(ctx, q, id).Scan(
		&u.ID, &u.Email, &u.FirstName, &u.LastName, &u.AvatarURL, &u.Permissions,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

// GetEtablissementByUAI implémente Store.GetEtablissementByUAI.
func (s *PgStore) GetEtablissementByUAI(ctx context.Context, uai string) (*models.Etablissement, error) {
	q := fmt.Sprintf(`SELECT %s FROM etablissement WHERE "UAI" = $1`, etabColumns)

	etab, err := scanEtablissement(s.pool.QueryRow(ctx, q, uai))
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	res, err := s.resultatsForUAI(ctx, etab.UAI)
	if err != nil {
		return nil, err
	}
	etab.Resultats = res

	return etab, nil
}

// FindEtablissementsInZone implémente Store.FindEtablissementsInZone.
func (s *PgStore) FindEtablissementsInZone(ctx context.Context, polygonWKT string, nature, secteur []string) ([]models.Etablissement, error) {
	var sb strings.Builder
	fmt.Fprintf(&sb, `SELECT %s FROM etablissement WHERE ST_Within(position, ST_GeomFromEWKT($1))`, etabColumns)

	args := []any{polygonWKT}
	if len(nature) > 0 {
		args = append(args, nature)
		fmt.Fprintf(&sb, " AND nature = ANY($%d)", len(args))
	}
	if len(secteur) > 0 {
		args = append(args, secteur)
		fmt.Fprintf(&sb, " AND secteur = ANY($%d)", len(args))
	}

	rows, err := s.pool.Query(ctx, sb.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var etabs []models.Etablissement
	for rows.Next() {
		e, err := scanEtablissement(rows)
		if err != nil {
			return nil, err
		}
		etabs = append(etabs, *e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Charge les résultats de chaque établissement.
	for i := range etabs {
		res, err := s.resultatsForUAI(ctx, etabs[i].UAI)
		if err != nil {
			return nil, err
		}
		etabs[i].Resultats = res
	}

	return etabs, nil
}

func (s *PgStore) resultatsForUAI(ctx context.Context, uai string) ([]models.Resultat, error) {
	const q = `SELECT idx, diplome, annee, presents, admis, mentions, etablissement_uai
		FROM resultat WHERE etablissement_uai = $1 ORDER BY annee DESC, diplome`

	rows, err := s.pool.Query(ctx, q, uai)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := []models.Resultat{}
	for rows.Next() {
		var r models.Resultat
		if err := rows.Scan(&r.Idx, &r.Diplome, &r.Annee, &r.Presents, &r.Admis, &r.Mentions, &r.EtablissementUAI); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// EnsureUserTable crée la table `utilisateur` si elle n'existe pas (migration idempotente).
func (s *PgStore) EnsureUserTable(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, createUserTableSQL)
	return err
}

// createUserTableSQL est la DDL de la table de profils.
const createUserTableSQL = `CREATE TABLE IF NOT EXISTS utilisateur (
	id          TEXT PRIMARY KEY,
	email       TEXT,
	first_name  TEXT,
	last_name   TEXT,
	avatar_url  TEXT,
	permissions TEXT[] NOT NULL DEFAULT '{}'
)`
