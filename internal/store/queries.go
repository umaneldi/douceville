package store

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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

// GetUserByEmail implémente Store.GetUserByEmail (hash du mot de passe inclus).
func (s *PgStore) GetUserByEmail(ctx context.Context, email string) (*models.UserRecord, error) {
	const q = `SELECT id, email, first_name, last_name, avatar_url, permissions, password_hash
		FROM utilisateur WHERE lower(email) = lower($1)`

	var u models.UserRecord
	err := s.pool.QueryRow(ctx, q, email).Scan(
		&u.ID, &u.Email, &u.FirstName, &u.LastName, &u.AvatarURL, &u.Permissions, &u.PasswordHash,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

// CreateUser implémente Store.CreateUser. L'identifiant est un UUID v4 généré
// côté application (il alimente le claim `sub` des JWT émis).
func (s *PgStore) CreateUser(ctx context.Context, email, passwordHash string, firstName, lastName *string) (*models.DvUser, error) {
	id, err := newUUID()
	if err != nil {
		return nil, err
	}

	const q = `INSERT INTO utilisateur (id, email, first_name, last_name, password_hash, permissions)
		VALUES ($1, $2, $3, $4, $5, '{}')
		RETURNING id, email, first_name, last_name, avatar_url, permissions`

	var u models.DvUser
	err = s.pool.QueryRow(ctx, q, id, email, firstName, lastName, passwordHash).Scan(
		&u.ID, &u.Email, &u.FirstName, &u.LastName, &u.AvatarURL, &u.Permissions,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
			return nil, ErrEmailTaken
		}
		return nil, err
	}
	return &u, nil
}

// newUUID génère un UUID v4 (RFC 4122) à partir de crypto/rand.
func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant RFC 4122
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
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
