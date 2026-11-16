-- Niveau d'accès d'un compte, par ordre chronologique / croissant :
--   unverified : compte créé, adresse e-mail non vérifiée ;
--   verified   : adresse vérifiée, en attente de validation par un administrateur ;
--   granted    : accès à l'application ;
--   admin      : administrateur.
-- L'ordre de déclaration de l'enum permet les comparaisons (perms >= 'granted').
DO $$ BEGIN
    CREATE TYPE user_perms AS ENUM ('unverified', 'verified', 'granted', 'admin');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- Les comptes existants au moment de l'ajout de la colonne conservent leur accès
-- (DEFAULT 'granted' appliqué uniquement lors de la création de la colonne), puis
-- le défaut passe à 'unverified' pour les nouveaux comptes. Rejouable sans effet.
ALTER TABLE utilisateur ADD COLUMN IF NOT EXISTS perms user_perms NOT NULL DEFAULT 'granted';
ALTER TABLE utilisateur ALTER COLUMN perms SET DEFAULT 'unverified';

-- Jetons de vérification d'e-mail (seul le hash SHA-256 du jeton est stocké).
CREATE TABLE IF NOT EXISTS email_verification (
    token_hash TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES utilisateur(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS email_verification_user_id ON email_verification (user_id);

-- JWT révoqués par déconnexion (claim `jti`), conservés jusqu'à leur expiration.
CREATE TABLE IF NOT EXISTS revoked_token (
    jti        TEXT PRIMARY KEY,
    expires_at TIMESTAMPTZ NOT NULL
);
