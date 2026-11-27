package store

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/umaneldi/douceville/internal/models"
)

// etabCols liste les colonnes d'un établissement, préfixées par alias (« e. »,
// « s. » ou vide), dans l'ordre de etabDest ; position est remplacée par
// l'expression pos (GeoJSON).
func etabCols(alias, pos string) string {
	cols := []string{`id`, `"UAI"`, `nom`, `adresse`, `lieu_dit`, `code_postal`, `commune`, ``,
		`departement`, `academie`, `secteur`, `ouverture`, `nature`, `filieres`,
		`education_prioritaire`, `restauration`, `hebergement`, `ulis`, `segpa`, `apprentissage`,
		`post_bac`, `lycee_des_metiers`, `sections`, `telephone`, `mail`, `site_web`, `fiche_onisep`}
	for i, c := range cols {
		if c == `` {
			cols[i] = pos
		} else {
			cols[i] = alias + c
		}
	}
	return strings.Join(cols, ", ")
}

// etabDest renvoie les destinations de Scan des colonnes de etabCols.
func etabDest(e *models.Etablissement) []any {
	return []any{
		&e.ID, &e.UAI, &e.Nom, &e.Adresse, &e.LieuDit, &e.CodePostal, &e.Commune,
		&e.Position, &e.Departement, &e.Academie, &e.Secteur, &e.Ouverture, &e.Nature, &e.Filieres,
		&e.EducationPrioritaire, &e.Restauration, &e.Hebergement, &e.ULIS, &e.SEGPA, &e.Apprentissage,
		&e.PostBac, &e.LyceeDesMetiers, &e.Sections, &e.Telephone, &e.Mail, &e.SiteWeb, &e.FicheOnisep,
	}
}

// colonnes sélectionnées pour un établissement. position est émise en GeoJSON.
var etabColumns = etabCols("", "ST_AsGeoJSON(position) AS position")

func scanEtablissement(row pgx.Row) (*models.Etablissement, error) {
	var e models.Etablissement
	if err := row.Scan(etabDest(&e)...); err != nil {
		return nil, err
	}
	return &e, nil
}

// userCols liste les colonnes du profil public (table utilisateur, alias u).
const userCols = `u.id, u.email, u.first_name, u.last_name, u.avatar_url, u.permissions, u.perms::text,
	u.password_hash IS NOT NULL,
	(SELECT v.new_email FROM email_verification v
	  WHERE v.user_id = u.id AND v.new_email IS NOT NULL AND v.expires_at > now() LIMIT 1),
	u.balance_cents`

func scanUser(row pgx.Row) (*models.DvUser, error) {
	var u models.DvUser
	err := row.Scan(&u.ID, &u.Email, &u.FirstName, &u.LastName, &u.AvatarURL, &u.Permissions, &u.Perms,
		&u.HasPassword, &u.PendingEmail, &u.BalanceCents)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

// GetUser implémente Store.GetUser.
func (s *PgStore) GetUser(ctx context.Context, id string) (*models.DvUser, error) {
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userCols+` FROM utilisateur u WHERE u.id = $1`, id))
}

// GetUserRecordByID implémente Store.GetUserRecordByID.
func (s *PgStore) GetUserRecordByID(ctx context.Context, id string) (*models.UserRecord, error) {
	const q = `SELECT id, email, first_name, last_name, avatar_url, permissions, perms::text,
		password_hash IS NOT NULL, COALESCE(password_hash, ''), balance_cents
		FROM utilisateur WHERE id = $1`

	var u models.UserRecord
	err := s.pool.QueryRow(ctx, q, id).Scan(
		&u.ID, &u.Email, &u.FirstName, &u.LastName, &u.AvatarURL, &u.Permissions, &u.Perms, &u.HasPassword, &u.PasswordHash, &u.BalanceCents,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

// UpdateProfile implémente Store.UpdateProfile.
func (s *PgStore) UpdateProfile(ctx context.Context, id string, p models.ProfileUpdate) (*models.DvUser, error) {
	return scanUser(s.pool.QueryRow(ctx, `UPDATE utilisateur u SET first_name = $2, last_name = $3, avatar_url = $4
		WHERE u.id = $1 RETURNING `+userCols, id, p.FirstName, p.LastName, p.AvatarURL))
}

// ListUsers implémente Store.ListUsers.
func (s *PgStore) ListUsers(ctx context.Context) ([]models.DvUser, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+userCols+` FROM utilisateur u ORDER BY lower(u.email), u.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []models.DvUser{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, *u)
	}
	return users, rows.Err()
}

// SetUserPerms implémente Store.SetUserPerms. Le niveau précédent est lu sous
// verrou dans la même requête, pour qu'il corresponde bien à ce changement.
func (s *PgStore) SetUserPerms(ctx context.Context, id string, perms models.Perms) (*models.DvUser, models.Perms, error) {
	var u models.DvUser
	var previous models.Perms
	err := s.pool.QueryRow(ctx, `UPDATE utilisateur u SET perms = $2::user_perms
		FROM (SELECT id, perms AS old FROM utilisateur WHERE id = $1 FOR UPDATE) o
		WHERE u.id = o.id
		RETURNING `+userCols+`, o.old::text`, id, string(perms)).Scan(
		&u.ID, &u.Email, &u.FirstName, &u.LastName, &u.AvatarURL, &u.Permissions, &u.Perms,
		&u.HasPassword, &u.PendingEmail, &u.BalanceCents, &previous)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, "", nil
		}
		return nil, "", err
	}
	return &u, previous, nil
}

// DeleteUser implémente Store.DeleteUser.
func (s *PgStore) DeleteUser(ctx context.Context, id string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM utilisateur WHERE id = $1`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// SetPassword implémente Store.SetPassword.
func (s *PgStore) SetPassword(ctx context.Context, id, passwordHash string) error {
	_, err := s.pool.Exec(ctx, `UPDATE utilisateur SET password_hash = $2 WHERE id = $1`, id, passwordHash)
	return err
}

// CreateEmailChange implémente Store.CreateEmailChange.
func (s *PgStore) CreateEmailChange(ctx context.Context, userID, newEmail, tokenHash string, expiresAt time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // sans effet après Commit

	if _, err := tx.Exec(ctx, `DELETE FROM email_verification
		WHERE (user_id = $1 AND new_email IS NOT NULL) OR expires_at < now()`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO email_verification (token_hash, user_id, expires_at, new_email) VALUES ($1, $2, $3, $4)`,
		tokenHash, userID, expiresAt, newEmail,
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// GetUserByEmail implémente Store.GetUserByEmail (hash du mot de passe inclus).
func (s *PgStore) GetUserByEmail(ctx context.Context, email string) (*models.UserRecord, error) {
	const q = `SELECT id, email, first_name, last_name, avatar_url, permissions, perms::text, COALESCE(password_hash, ''), balance_cents
		FROM utilisateur WHERE lower(email) = lower($1)`

	var u models.UserRecord
	err := s.pool.QueryRow(ctx, q, email).Scan(
		&u.ID, &u.Email, &u.FirstName, &u.LastName, &u.AvatarURL, &u.Permissions, &u.Perms, &u.PasswordHash, &u.BalanceCents,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	u.HasPassword = u.PasswordHash != ""
	return &u, nil
}

// CreateUser implémente Store.CreateUser. L'identifiant est un UUID v4 généré
// côté application (il alimente le claim `sub` des JWT émis).
func (s *PgStore) CreateUser(ctx context.Context, email, passwordHash string, firstName, lastName *string) (*models.DvUser, error) {
	id, err := newUUID()
	if err != nil {
		return nil, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // sans effet après Commit

	const q = `INSERT INTO utilisateur (id, email, first_name, last_name, password_hash, permissions)
		VALUES ($1, $2, $3, $4, $5, '{}')
		RETURNING id, email, first_name, last_name, avatar_url, permissions, perms::text`

	var u models.DvUser
	err = tx.QueryRow(ctx, q, id, email, firstName, lastName, passwordHash).Scan(
		&u.ID, &u.Email, &u.FirstName, &u.LastName, &u.AvatarURL, &u.Permissions, &u.Perms,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
			return nil, ErrEmailTaken
		}
		return nil, err
	}
	if u.BalanceCents, err = grantWelcomeCredit(ctx, tx, u.ID); err != nil {
		return nil, err
	}
	return &u, tx.Commit(ctx)
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
// cas (usage unique) ; s'il n'a pas expiré :
//   - jeton d'inscription : le compte passe à 'granted' (accès direct, sans
//     validation d'un administrateur ; un niveau supérieur n'est jamais
//     rétrogradé) ;
//   - jeton de changement d'adresse : l'e-mail du compte devient new_email.
func (s *PgStore) VerifyEmail(ctx context.Context, tokenHash string) (*models.DvUser, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // sans effet après Commit

	var (
		userID   string
		newEmail *string
		valid    bool
	)
	err = tx.QueryRow(ctx, `DELETE FROM email_verification WHERE token_hash = $1
		RETURNING user_id, new_email, expires_at > now()`, tokenHash).Scan(&userID, &newEmail, &valid)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !valid {
		// Jeton expiré : on le consomme quand même.
		return nil, false, tx.Commit(ctx)
	}

	var u *models.DvUser
	if newEmail != nil {
		var taken bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM utilisateur WHERE lower(email) = lower($1) AND id <> $2)`,
			*newEmail, userID).Scan(&taken); err != nil {
			return nil, false, err
		}
		if taken {
			// Adresse prise entre-temps : le jeton reste consommé.
			if err := tx.Commit(ctx); err != nil {
				return nil, false, err
			}
			return nil, false, ErrEmailTaken
		}
		u, err = scanUser(tx.QueryRow(ctx, `UPDATE utilisateur u SET email = $2
			WHERE u.id = $1 RETURNING `+userCols, userID, *newEmail))
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // course avec une autre inscription
			return nil, false, ErrEmailTaken
		}
	} else {
		u, err = scanUser(tx.QueryRow(ctx, `UPDATE utilisateur u SET perms = GREATEST(u.perms, 'granted'::user_perms)
			WHERE u.id = $1 RETURNING `+userCols, userID))
	}
	if err != nil || u == nil {
		return nil, false, err
	}
	return u, newEmail != nil, tx.Commit(ctx)
}

// PeekEmailVerification implémente Store.PeekEmailVerification.
func (s *PgStore) PeekEmailVerification(ctx context.Context, tokenHash string) (string, string, bool, error) {
	var userID string
	var email *string
	err := s.pool.QueryRow(ctx, `SELECT v.user_id, COALESCE(v.new_email, u.email)
		FROM email_verification v JOIN utilisateur u ON u.id = v.user_id
		WHERE v.token_hash = $1 AND v.expires_at > now()`, tokenHash).Scan(&userID, &email)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	e := ""
	if email != nil {
		e = *email
	}
	return userID, e, true, nil
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

// FindOrCreateOIDCUser implémente Store.FindOrCreateOIDCUser.
func (s *PgStore) FindOrCreateOIDCUser(ctx context.Context, id models.OIDCIdentity, defaultPerms models.Perms) (*models.DvUser, error) {
	const cols = `u.id, u.email, u.first_name, u.last_name, u.avatar_url, u.permissions, u.perms::text, u.balance_cents`
	scan := func(row pgx.Row) (*models.DvUser, error) {
		var u models.DvUser
		if err := row.Scan(&u.ID, &u.Email, &u.FirstName, &u.LastName, &u.AvatarURL, &u.Permissions, &u.Perms, &u.BalanceCents); err != nil {
			return nil, err
		}
		return &u, nil
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // sans effet après Commit

	// 1. Identité déjà connue.
	u, err := scan(tx.QueryRow(ctx, `SELECT `+cols+` FROM oidc_identity i JOIN utilisateur u ON u.id = i.user_id
		WHERE i.issuer = $1 AND i.subject = $2`, id.Issuer, id.Subject))
	if err == nil {
		return u, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	linkIdentity := func(userID string) error {
		_, err := tx.Exec(ctx, `INSERT INTO oidc_identity (issuer, subject, user_id) VALUES ($1, $2, $3)`,
			id.Issuer, id.Subject, userID)
		return err
	}

	// 2. Compte existant avec le même e-mail : rattachement si l'adresse est attestée.
	if id.Email != "" {
		var existing string
		err := tx.QueryRow(ctx, `SELECT id FROM utilisateur WHERE lower(email) = lower($1) FOR UPDATE`, id.Email).Scan(&existing)
		switch {
		case err == nil:
			if !id.EmailVerified {
				return nil, ErrEmailTaken
			}
			if err := linkIdentity(existing); err != nil {
				return nil, err
			}
			// Le fournisseur atteste l'adresse : le compte obtient l'accès, comme
			// après la vérification de l'e-mail.
			u, err := scan(tx.QueryRow(ctx, `UPDATE utilisateur u SET perms = GREATEST(u.perms, 'granted'::user_perms)
				WHERE u.id = $1 RETURNING `+cols, existing))
			if err != nil {
				return nil, err
			}
			return u, tx.Commit(ctx)
		case !errors.Is(err, pgx.ErrNoRows):
			return nil, err
		}
	}

	// 3. Nouveau compte (sans mot de passe).
	newID, err := newUUID()
	if err != nil {
		return nil, err
	}
	u, err = scan(tx.QueryRow(ctx, `INSERT INTO utilisateur AS u (id, email, first_name, last_name, avatar_url, permissions, perms)
		VALUES ($1, $2, $3, $4, $5, '{}', $6::user_perms)
		RETURNING `+cols,
		newID, nilIfEmpty(id.Email), nilIfEmpty(id.GivenName), nilIfEmpty(id.FamilyName), nilIfEmpty(id.Picture), string(defaultPerms)))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation (e-mail)
			return nil, ErrEmailTaken
		}
		return nil, err
	}
	if err := linkIdentity(u.ID); err != nil {
		return nil, err
	}
	if u.BalanceCents, err = grantWelcomeCredit(ctx, tx, u.ID); err != nil {
		return nil, err
	}
	return u, tx.Commit(ctx)
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
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

// secuDepJoin joint, à une ligne s de securite_score, son score départemental
// et le classement de son département (vue securite_departement, alias sd) et
// le nom du département (table departement, alias dep).
const secuDepJoin = `LEFT JOIN securite_departement sd ON sd.code_commune = s.code_commune
			AND sd.annee = s.annee
		LEFT JOIN departement dep ON dep.code = sd.departement`

// secuDepColumns liste les colonnes de secuDepJoin lues par secuDep.dest.
const secuDepColumns = `sd.score_departement, sd.departement, dep.nom, sd.dep_score_moyen, sd.dep_rang, sd.nb_departements`

// secuDep reçoit les colonnes secuDepColumns (toutes nulles sans ligne dans
// securite_departement).
type secuDep struct {
	score, rang, nb *int
	code, nom       *string
	moyen           *float64
}

// dest renvoie les destinations de Scan, dans l'ordre de secuDepColumns.
func (d *secuDep) dest() []any {
	return []any{&d.score, &d.code, &d.nom, &d.moyen, &d.rang, &d.nb}
}

// departement construit le classement du département (nil sans données).
func (d *secuDep) departement() *models.DepartementSecurite {
	if d.code == nil || d.moyen == nil || d.rang == nil || d.nb == nil {
		return nil
	}
	return &models.DepartementSecurite{Code: *d.code, Nom: d.nom, ScoreMoyen: *d.moyen, Rang: *d.rang, NbDepartements: *d.nb}
}

// FindCommunesInZone implémente Store.FindCommunesInZone. Le WKT de
// l'isochrone n'a pas de SRID : il est fixé à 4326 pour correspondre à
// commune.contour. La fiscalité est celle du dernier exercice de la table
// fiscalite (nulle si la commune n'y figure pas ou sans taux TFB).
func (s *PgStore) FindCommunesInZone(ctx context.Context, polygonWKT string) ([]models.CommuneSecurite, error) {
	const q = `SELECT c.code_commune, c.nom, s.annee, s.score, s.nb_indicateurs,
			` + secuDepColumns + `,
			f.year, f.taux_global_tfb, f.taux_plein_teom,
			ST_AsGeoJSON(c.contour, 5)
		FROM commune c
		LEFT JOIN securite_score s ON s.code_commune = c.code_commune
			AND s.annee = (SELECT max(annee) FROM securite_score)
		` + secuDepJoin + `
		LEFT JOIN fiscalite f ON f.code_commune = c.code_commune
			AND f.year = (SELECT max(year) FROM fiscalite)
		WHERE c.contour IS NOT NULL
			AND ST_Intersects(c.contour, ST_SetSRID(ST_GeomFromText($1), 4326))
		ORDER BY c.code_commune`

	rows, err := s.pool.Query(ctx, q, polygonWKT)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	communes := []models.CommuneSecurite{}
	for rows.Next() {
		var c models.CommuneSecurite
		var contour string
		var fYear *int
		var tfb, teom *float64
		var dep secuDep
		if err := rows.Scan(append(append([]any{&c.CodeCommune, &c.Nom, &c.Annee, &c.Score, &c.NbIndicateurs},
			dep.dest()...), &fYear, &tfb, &teom, &contour)...); err != nil {
			return nil, err
		}
		c.ScoreDepartement, c.Departement = dep.score, dep.departement()
		if fYear != nil && tfb != nil {
			f := models.Fiscalite{Annee: *fYear, TauxTFB: *tfb, TauxTEOM: teom, Taux: *tfb}
			if teom != nil {
				f.Taux += *teom
			}
			c.Fiscalite = &f
		}
		c.Contour = json.RawMessage(contour)
		communes = append(communes, c)
	}
	return communes, rows.Err()
}

// FindImmobilierInZone implémente Store.FindImmobilierInZone. Le prix est la
// médiane de valeur_fonciere / surface_reelle_bati sur toutes les ventes de
// immobilier ; celui d'une commune porte sur tous ses IRIS (pas seulement
// ceux de la zone).
func (s *PgStore) FindImmobilierInZone(ctx context.Context, polygonWKT string, codesCommunes []string) (models.ImmobilierZone, error) {
	zone := models.ImmobilierZone{
		Communes:  []models.CommuneImmobilier{},
		Quartiers: []models.QuartierImmobilier{},
	}
	if err := s.pool.QueryRow(ctx,
		`SELECT min(date_mutation)::text, max(date_mutation)::text FROM immobilier`,
	).Scan(&zone.DateMin, &zone.DateMax); err != nil {
		return zone, err
	}
	if zone.DateMin == nil {
		return zone, nil // table vide
	}

	const qQuartiers = `SELECT q.iris, q.code_commune, q.nom_iris, p.prix, p.ventes,
			ST_AsGeoJSON(q.contour, 5)
		FROM quartier q
		CROSS JOIN LATERAL (
			SELECT round(percentile_cont(0.5) WITHIN GROUP (
					ORDER BY i.valeur_fonciere / i.surface_reelle_bati))::integer AS prix,
				count(*)::integer AS ventes
			FROM immobilier i WHERE i.iris = q.iris
		) p
		WHERE ST_Intersects(q.contour, ST_SetSRID(ST_GeomFromText($1), 4326))
		ORDER BY q.iris`
	rows, err := s.pool.Query(ctx, qQuartiers, polygonWKT)
	if err != nil {
		return zone, err
	}
	for rows.Next() {
		var q models.QuartierImmobilier
		var prix *int
		var ventes int
		var contour string
		if err := rows.Scan(&q.Iris, &q.CodeCommune, &q.Nom, &prix, &ventes, &contour); err != nil {
			rows.Close()
			return zone, err
		}
		if prix != nil {
			q.Prix = &models.PrixM2{PrixM2: *prix, Ventes: ventes}
		}
		q.Contour = json.RawMessage(contour)
		zone.Quartiers = append(zone.Quartiers, q)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return zone, err
	}

	if len(codesCommunes) == 0 {
		return zone, nil
	}
	const qCommunes = `SELECT q.code_commune,
			round(percentile_cont(0.5) WITHIN GROUP (
				ORDER BY i.valeur_fonciere / i.surface_reelle_bati))::integer,
			count(*)::integer
		FROM quartier q
		JOIN immobilier i ON i.iris = q.iris
		WHERE q.code_commune = ANY($1)
		GROUP BY q.code_commune
		ORDER BY q.code_commune`
	rows, err = s.pool.Query(ctx, qCommunes, codesCommunes)
	if err != nil {
		return zone, err
	}
	defer rows.Close()
	for rows.Next() {
		var c models.CommuneImmobilier
		var p models.PrixM2
		if err := rows.Scan(&c.CodeCommune, &p.PrixM2, &p.Ventes); err != nil {
			return zone, err
		}
		c.Prix = &p
		zone.Communes = append(zone.Communes, c)
	}
	return zone, rows.Err()
}

// FindLogementsSociauxInZone implémente Store.FindLogementsSociauxInZone.
// position est en SRID 0 comme etablissement.position : même prédicat que
// FindEtablissementsInZone. Les logements identiques d'une position sont
// comptés ensemble, puis les lignes (triées par position) sont regroupées.
func (s *PgStore) FindLogementsSociauxInZone(ctx context.Context, polygonWKT string) ([]models.LogementSocialPosition, error) {
	const q = `SELECT ST_X(position), ST_Y(position), type_construction,
			type_droit_immobilier, nb_pieces, surface, annee_construction,
			count(*)::integer
		FROM logement_social
		WHERE position IS NOT NULL AND ST_Within(position, ST_GeomFromEWKT($1))
		GROUP BY 1, 2, 3, 4, 5, 6, 7
		ORDER BY 1, 2, nb_pieces NULLS LAST, surface NULLS LAST`

	rows, err := s.pool.Query(ctx, q, polygonWKT)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	positions := []models.LogementSocialPosition{}
	for rows.Next() {
		var lon, lat float64
		var g models.LogementSocialGroupe
		if err := rows.Scan(&lon, &lat, &g.TypeConstruction, &g.TypeDroit,
			&g.NbPieces, &g.Surface, &g.AnneeConstruction, &g.Nb); err != nil {
			return nil, err
		}
		n := len(positions)
		if n == 0 || positions[n-1].Lon != lon || positions[n-1].Lat != lat {
			positions = append(positions, models.LogementSocialPosition{Lon: lon, Lat: lat})
			n++
		}
		p := &positions[n-1]
		p.NbLogements += g.Nb
		p.Logements = append(p.Logements, g)
	}
	return positions, rows.Err()
}

// IntersectUnions implémente Store.IntersectUnions. Les polygones sont rendus
// valides (ST_MakeValid), unis au sein de chaque groupe (ST_Union), puis les
// unions sont intersectées successivement ; seules les parties surfaciques
// sont conservées (des isochrones qui se touchent peuvent donner des lignes
// ou des points).
func (s *PgStore) IntersectUnions(ctx context.Context, groups [][]string) (string, json.RawMessage, bool, error) {
	if len(groups) == 0 {
		return "", nil, true, errors.New("IntersectUnions: no group")
	}
	var args []any
	expr := ""
	for i, group := range groups {
		if len(group) == 0 {
			return "", nil, true, fmt.Errorf("IntersectUnions: group %d is empty", i)
		}
		parts := make([]string, len(group))
		for j, wkt := range group {
			args = append(args, wkt)
			parts[j] = fmt.Sprintf("ST_MakeValid(ST_GeomFromText($%d))", len(args))
		}
		union := "ST_Union(ARRAY[" + strings.Join(parts, ", ") + "])"
		if expr == "" {
			expr = union
		} else {
			expr = fmt.Sprintf("ST_Intersection(%s, %s)", expr, union)
		}
	}
	q := fmt.Sprintf(`SELECT ST_IsEmpty(g), ST_AsText(g), ST_AsGeoJSON(g, 6)
		FROM (SELECT ST_CollectionExtract(%s, 3) AS g) z`, expr)

	var empty bool
	var wkt, gj string
	if err := s.pool.QueryRow(ctx, q, args...).Scan(&empty, &wkt, &gj); err != nil {
		return "", nil, true, err
	}
	if empty {
		return "", nil, true, nil
	}
	return wkt, json.RawMessage(gj), false, nil
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
