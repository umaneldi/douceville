package store

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/umaneldi/douceville/internal/models"
)

func TestNewUUID(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	seen := map[string]bool{}
	for range 100 {
		id, err := newUUID()
		if err != nil {
			t.Fatal(err)
		}
		if !re.MatchString(id) {
			t.Fatalf("%q is not a v4 UUID", id)
		}
		if seen[id] {
			t.Fatalf("duplicate UUID %q", id)
		}
		seen[id] = true
	}
}

func TestNilIfEmpty(t *testing.T) {
	if nilIfEmpty("") != nil {
		t.Error("empty string should give nil")
	}
	if p := nilIfEmpty("x"); p == nil || *p != "x" {
		t.Errorf("got %v", p)
	}
}

func TestCanMigrateIntegration(t *testing.T) {
	s, pool := newTestStore(t)
	ctx := context.Background()
	if ok, err := s.CanMigrate(ctx); err != nil || !ok {
		t.Fatalf("owner role: CanMigrate = %v, %v", ok, err)
	}

	// Rôle sans droit CREATE sur le schéma : les migrations doivent être sautées.
	role := pool.Config().ConnConfig.Database + "_reader"
	mustExec(t, pool, "CREATE ROLE "+role)
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), "DROP ROLE IF EXISTS "+role); err != nil {
			t.Errorf("drop role: %v", err)
		}
	})
	cfg := pool.Config().Copy()
	cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		_, err := c.Exec(ctx, "SET ROLE "+role)
		return err
	}
	reader, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if ok, err := New(reader).CanMigrate(ctx); err != nil || ok {
		t.Errorf("read-only role: CanMigrate = %v, %v", ok, err)
	}
}

func TestUsersIntegration(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	u, err := s.CreateUser(ctx, "Alice@Test.invalid", "hash-a", new("Alice"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if u.Perms != models.PermsUnverified || *u.FirstName != "Alice" || u.LastName != nil {
		t.Errorf("created = %+v", u)
	}
	if _, err := s.CreateUser(ctx, "alice@test.invalid", "h", nil, nil); !errors.Is(err, ErrEmailTaken) {
		t.Errorf("duplicate (case-insensitive) e-mail: err = %v", err)
	}
	bob, err := s.CreateUser(ctx, "bob@test.invalid", "hash-b", nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Lecture.
	if got, err := s.GetUser(ctx, u.ID); err != nil || got == nil || !got.HasPassword || got.PendingEmail != nil {
		t.Errorf("GetUser = %+v, %v", got, err)
	}
	if got, err := s.GetUser(ctx, "nobody"); got != nil || err != nil {
		t.Errorf("GetUser(unknown) = %+v, %v", got, err)
	}
	if rec, err := s.GetUserByEmail(ctx, "ALICE@test.INVALID"); err != nil || rec == nil || rec.ID != u.ID || rec.PasswordHash != "hash-a" {
		t.Errorf("GetUserByEmail = %+v, %v", rec, err)
	}
	if rec, err := s.GetUserByEmail(ctx, "nobody@test.invalid"); rec != nil || err != nil {
		t.Errorf("GetUserByEmail(unknown) = %+v, %v", rec, err)
	}
	if rec, err := s.GetUserRecordByID(ctx, u.ID); err != nil || rec.PasswordHash != "hash-a" || !rec.HasPassword {
		t.Errorf("GetUserRecordByID = %+v, %v", rec, err)
	}
	if rec, err := s.GetUserRecordByID(ctx, "nobody"); rec != nil || err != nil {
		t.Errorf("GetUserRecordByID(unknown) = %+v, %v", rec, err)
	}

	// Profil et mot de passe.
	p, err := s.UpdateProfile(ctx, u.ID, models.ProfileUpdate{LastName: new("Martin"), AvatarURL: new("https://img")})
	if err != nil || p.FirstName != nil || *p.LastName != "Martin" || *p.AvatarURL != "https://img" {
		t.Errorf("UpdateProfile = %+v, %v", p, err)
	}
	if err := s.SetPassword(ctx, u.ID, "hash-a2"); err != nil {
		t.Fatal(err)
	}
	if rec, _ := s.GetUserRecordByID(ctx, u.ID); rec.PasswordHash != "hash-a2" {
		t.Errorf("password not updated: %q", rec.PasswordHash)
	}

	// Niveaux d'accès.
	got, prev, err := s.SetUserPerms(ctx, u.ID, models.PermsAdmin)
	if err != nil || got.Perms != models.PermsAdmin || prev != models.PermsUnverified {
		t.Errorf("SetUserPerms = %+v, %q, %v", got, prev, err)
	}
	if got, prev, err := s.SetUserPerms(ctx, "nobody", models.PermsAdmin); got != nil || prev != "" || err != nil {
		t.Errorf("SetUserPerms(unknown) = %+v, %q, %v", got, prev, err)
	}

	// Liste triée par e-mail (insensible à la casse).
	users, err := s.ListUsers(ctx)
	if err != nil || len(users) != 2 || users[0].ID != u.ID || users[1].ID != bob.ID {
		t.Errorf("ListUsers = %+v, %v", users, err)
	}

	// Suppression.
	if ok, err := s.DeleteUser(ctx, bob.ID); !ok || err != nil {
		t.Errorf("DeleteUser = %v, %v", ok, err)
	}
	if ok, err := s.DeleteUser(ctx, bob.ID); ok || err != nil {
		t.Errorf("DeleteUser(again) = %v, %v", ok, err)
	}
}

func TestEmailVerificationIntegration(t *testing.T) {
	s, pool := newTestStore(t)
	ctx := context.Background()
	future := time.Now().Add(time.Hour)

	u, err := s.CreateUser(ctx, "carol@test.invalid", "h", nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Un nouvel envoi remplace le jeton précédent.
	if err := s.CreateEmailVerification(ctx, u.ID, "tok-1", future); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateEmailVerification(ctx, u.ID, "tok-2", future); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, _ := s.PeekEmailVerification(ctx, "tok-1"); ok {
		t.Error("tok-1 should have been replaced")
	}
	id, email, ok, err := s.PeekEmailVerification(ctx, "tok-2")
	if err != nil || !ok || id != u.ID || email != "carol@test.invalid" {
		t.Errorf("Peek = %q %q %v %v", id, email, ok, err)
	}

	// Vérification : accès accordé, jeton à usage unique.
	got, changed, err := s.VerifyEmail(ctx, "tok-2")
	if err != nil || got == nil || changed || got.Perms != models.PermsGranted {
		t.Fatalf("VerifyEmail = %+v, %v, %v", got, changed, err)
	}
	if got, _, err := s.VerifyEmail(ctx, "tok-2"); got != nil || err != nil {
		t.Errorf("token reused: %+v, %v", got, err)
	}

	// Un administrateur n'est jamais rétrogradé par une vérification.
	if _, _, err := s.SetUserPerms(ctx, u.ID, models.PermsAdmin); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateEmailVerification(ctx, u.ID, "tok-3", future); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := s.VerifyEmail(ctx, "tok-3"); got == nil || got.Perms != models.PermsAdmin {
		t.Errorf("admin downgraded: %+v", got)
	}

	// Jeton expiré : invisible et consommé sans effet.
	mustExec(t, pool, `INSERT INTO email_verification (token_hash, user_id, expires_at) VALUES ('old', $1, now() - interval '1 hour')`, u.ID)
	if _, _, ok, _ := s.PeekEmailVerification(ctx, "old"); ok {
		t.Error("expired token visible")
	}
	if got, _, err := s.VerifyEmail(ctx, "old"); got != nil || err != nil {
		t.Errorf("expired token accepted: %+v, %v", got, err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM email_verification WHERE token_hash = 'old'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("expired token not consumed (count=%d, err=%v)", n, err)
	}
	if got, _, err := s.VerifyEmail(ctx, "unknown"); got != nil || err != nil {
		t.Errorf("unknown token: %+v, %v", got, err)
	}
}

func TestEmailChangeIntegration(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	future := time.Now().Add(time.Hour)

	u, err := s.CreateUser(ctx, "dave@test.invalid", "h", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateUser(ctx, "erin@test.invalid", "h", nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.CreateEmailChange(ctx, u.ID, "dave2@test.invalid", "chg-1", future); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetUser(ctx, u.ID); got.PendingEmail == nil || *got.PendingEmail != "dave2@test.invalid" || *got.Email != "dave@test.invalid" {
		t.Errorf("pending e-mail: %+v", got)
	}
	if _, email, ok, _ := s.PeekEmailVerification(ctx, "chg-1"); !ok || email != "dave2@test.invalid" {
		t.Errorf("Peek(change) = %q, %v", email, ok)
	}
	got, changed, err := s.VerifyEmail(ctx, "chg-1")
	if err != nil || !changed || *got.Email != "dave2@test.invalid" || got.PendingEmail != nil {
		t.Fatalf("VerifyEmail(change) = %+v, %v, %v", got, changed, err)
	}
	if got.Perms != models.PermsUnverified {
		t.Errorf("e-mail change must not change perms: %q", got.Perms)
	}

	// Adresse prise entre-temps par un autre compte.
	if err := s.CreateEmailChange(ctx, u.ID, "taken@test.invalid", "chg-2", future); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateEmailChange(ctx, other.ID, "x@test.invalid", "chg-3", future); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser(ctx, "TAKEN@test.invalid", "h", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.VerifyEmail(ctx, "chg-2"); !errors.Is(err, ErrEmailTaken) {
		t.Errorf("taken address: err = %v", err)
	}
	if _, _, ok, _ := s.PeekEmailVerification(ctx, "chg-2"); ok {
		t.Error("token for a taken address should be consumed")
	}
}

func TestRevokedTokensIntegration(t *testing.T) {
	s, pool := newTestStore(t)
	ctx := context.Background()

	if revoked, err := s.IsTokenRevoked(ctx, "jti-1"); revoked || err != nil {
		t.Fatalf("fresh token revoked: %v, %v", revoked, err)
	}
	mustExec(t, pool, `INSERT INTO revoked_token (jti, expires_at) VALUES ('stale', now() - interval '1 day')`)
	for range 2 { // idempotent
		if err := s.RevokeToken(ctx, "jti-1", time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if revoked, err := s.IsTokenRevoked(ctx, "jti-1"); !revoked || err != nil {
		t.Errorf("revoked token not detected: %v, %v", revoked, err)
	}
	if revoked, _ := s.IsTokenRevoked(ctx, "stale"); revoked {
		t.Error("expired revocation should be purged")
	}
}

func TestOIDCUserIntegration(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	id := models.OIDCIdentity{Issuer: "https://idp", Subject: "sub-1", Email: "frank@test.invalid",
		GivenName: "Frank", Picture: "https://pic"}
	u, err := s.FindOrCreateOIDCUser(ctx, id, models.PermsVerified)
	if err != nil {
		t.Fatal(err)
	}
	if u.Perms != models.PermsVerified || *u.FirstName != "Frank" || u.LastName != nil || *u.AvatarURL != "https://pic" {
		t.Errorf("created = %+v", u)
	}
	if rec, _ := s.GetUserRecordByID(ctx, u.ID); rec.HasPassword {
		t.Error("OIDC account must have no password")
	}
	// Identité déjà connue : même compte, même si l'e-mail a changé chez le fournisseur.
	id.Email = "frank-new@test.invalid"
	if again, err := s.FindOrCreateOIDCUser(ctx, id, models.PermsVerified); err != nil || again.ID != u.ID {
		t.Errorf("known identity = %+v, %v", again, err)
	}

	// Compte local existant : rattachement seulement si l'adresse est attestée.
	local, err := s.CreateUser(ctx, "grace@test.invalid", "h", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	other := models.OIDCIdentity{Issuer: "https://idp", Subject: "sub-2", Email: "GRACE@test.invalid"}
	if _, err := s.FindOrCreateOIDCUser(ctx, other, models.PermsVerified); !errors.Is(err, ErrEmailTaken) {
		t.Errorf("unverified e-mail: err = %v", err)
	}
	other.EmailVerified = true
	linked, err := s.FindOrCreateOIDCUser(ctx, other, models.PermsVerified)
	if err != nil || linked.ID != local.ID || linked.Perms != models.PermsGranted {
		t.Errorf("linked = %+v, %v", linked, err)
	}

	// Identité sans e-mail : nouveau compte.
	anon, err := s.FindOrCreateOIDCUser(ctx, models.OIDCIdentity{Issuer: "https://idp", Subject: "sub-3"}, models.PermsGranted)
	if err != nil || anon.Email != nil || anon.Perms != models.PermsGranted {
		t.Errorf("no e-mail = %+v, %v", anon, err)
	}

	// La suppression du compte supprime ses identités.
	if ok, err := s.DeleteUser(ctx, u.ID); !ok || err != nil {
		t.Fatal(err)
	}
	id.Email = ""
	if fresh, err := s.FindOrCreateOIDCUser(ctx, id, models.PermsVerified); err != nil || fresh.ID == u.ID {
		t.Errorf("identity survived account deletion: %+v, %v", fresh, err)
	}
}
