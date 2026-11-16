package store

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"

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
	const q = `SELECT id, email, first_name, last_name, avatar_url, permissions, perms::text
		FROM utilisateur WHERE id = $1`

	var u models.DvUser
	err := s.pool.QueryRow(ctx, q, id).Scan(
		&u.ID, &u.Email, &u.FirstName, &u.LastName, &u.AvatarURL, &u.Permissions, &u.Perms,
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
	const q = `SELECT id, email, first_name, last_name, avatar_url, permissions, perms::text, password_hash
		FROM utilisateur WHERE lower(email) = lower($1)`

	var u models.UserRecord
	err := s.pool.QueryRow(ctx, q, email).Scan(
		&u.ID, &u.Email, &u.FirstName, &u.LastName, &u.AvatarURL, &u.Permissions, &u.Perms, &u.PasswordHash,
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
		RETURNING id, email, first_name, last_name, avatar_url, permissions, perms::text`

	var u models.DvUser
	err = s.pool.QueryRow(ctx, q, id, email, firstName, lastName, passwordHash).Scan(
		&u.ID, &u.Email, &u.FirstName, &u.LastName, &u.AvatarURL, &u.Permissions, &u.Perms,
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

// CreateEmailVerification implémente Store.CreateEmailVerification.
func (s *PgStore) CreateEmailVerification(ctx context.Context, userID, tokenHash string, expiresAt time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // sans effet après Commit

	if _, err := tx.Exec(ctx, `DELETE FROM email_verification WHERE user_id = $1 OR expires_at < now()`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO email_verification (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		tokenHash, userID, expiresAt,
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// VerifyEmail implémente Store.VerifyEmail. Le jeton est supprimé dans tous les
// cas (usage unique) ; s'il n'a pas expiré, un compte 'unverified' passe à
// 'verified' (un niveau supérieur n'est jamais rétrogradé).
func (s *PgStore) VerifyEmail(ctx context.Context, tokenHash string) (*models.DvUser, error) {
	const q = `WITH t AS (
			DELETE FROM email_verification WHERE token_hash = $1
			RETURNING user_id, expires_at
		)
		UPDATE utilisateur u SET perms = GREATEST(u.perms, 'verified'::user_perms)
		FROM t WHERE u.id = t.user_id AND t.expires_at > now()
		RETURNING u.id, u.email, u.first_name, u.last_name, u.avatar_url, u.permissions, u.perms::text`

	var u models.DvUser
	err := s.pool.QueryRow(ctx, q, tokenHash).Scan(
		&u.ID, &u.Email, &u.FirstName, &u.LastName, &u.AvatarURL, &u.Permissions, &u.Perms,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

// RevokeToken implémente Store.RevokeToken. Les révocations expirées sont
// purgées au passage.
func (s *PgStore) RevokeToken(ctx context.Context, jti string, expiresAt time.Time) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM revoked_token WHERE expires_at < now()`); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO revoked_token (jti, expires_at) VALUES ($1, $2) ON CONFLICT (jti) DO NOTHING`,
		jti, expiresAt,
	)
	return err
}

// IsTokenRevoked implémente Store.IsTokenRevoked.
func (s *PgStore) IsTokenRevoked(ctx context.Context, jti string) (bool, error) {
	var revoked bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM revoked_token WHERE jti = $1)`, jti).Scan(&revoked)
	return revoked, err
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
