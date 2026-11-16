-- Table des profils utilisateurs (auth native Douceville).
-- `id` est un UUID v4 généré par le backend ; il alimente le claim `sub` des JWT.
CREATE TABLE IF NOT EXISTS utilisateur (
    id          TEXT PRIMARY KEY,
    email       TEXT,
    first_name  TEXT,
    last_name   TEXT,
    avatar_url  TEXT,
    permissions TEXT[] NOT NULL DEFAULT '{}'
);
