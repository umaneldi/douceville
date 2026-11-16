-- Colonnes d'authentification native ajoutées à la table utilisateur.
ALTER TABLE utilisateur ADD COLUMN IF NOT EXISTS password_hash TEXT;

-- Unicité de l'email (insensible à la casse) pour la connexion.
CREATE UNIQUE INDEX IF NOT EXISTS utilisateur_email_unique
    ON utilisateur (lower(email));
