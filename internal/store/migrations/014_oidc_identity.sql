-- Identités OpenID Connect rattachées à un compte Douceville. Un compte peut
-- avoir plusieurs identités (fournisseurs) ; un compte créé via OIDC n'a pas
-- de mot de passe (password_hash NULL).
CREATE TABLE IF NOT EXISTS oidc_identity (
    issuer  TEXT NOT NULL,
    subject TEXT NOT NULL,
    user_id TEXT NOT NULL REFERENCES utilisateur(id) ON DELETE CASCADE,
    PRIMARY KEY (issuer, subject)
);

CREATE INDEX IF NOT EXISTS oidc_identity_user_id ON oidc_identity (user_id);
