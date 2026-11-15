-- Schéma des données métier : établissements scolaires et résultats d'examens.
-- Ces tables sont créées vides ; les données sont chargées hors application
-- (il n'existe plus d'ETL dans ce dépôt).
CREATE EXTENSION IF NOT EXISTS postgis;

CREATE TABLE IF NOT EXISTS etablissement (
    id          SERIAL PRIMARY KEY,
    "UAI"       TEXT NOT NULL UNIQUE,
    nom         TEXT NOT NULL,
    adresse     TEXT,
    lieu_dit    TEXT,
    code_postal TEXT NOT NULL,
    commune     TEXT NOT NULL,
    -- SRID 0 : cohérent avec les isochrones (ST_GeomFromEWKT sans SRID).
    position    geometry(Point) NOT NULL,
    departement TEXT NOT NULL,
    academie    TEXT,
    secteur     TEXT NOT NULL,
    ouverture   TIMESTAMPTZ,
    nature      TEXT NOT NULL
);

-- Index spatial pour accélérer ST_Within sur la zone d'accessibilité.
CREATE INDEX IF NOT EXISTS etablissement_position_gist
    ON etablissement USING GIST (position);

CREATE TABLE IF NOT EXISTS resultat (
    idx               SERIAL PRIMARY KEY,
    diplome           TEXT NOT NULL,
    annee             INTEGER NOT NULL,
    presents          INTEGER,
    admis             INTEGER,
    mentions          INTEGER,
    etablissement_uai TEXT REFERENCES etablissement ("UAI") ON DELETE CASCADE,
    UNIQUE (diplome, annee, etablissement_uai)
);

CREATE INDEX IF NOT EXISTS resultat_etablissement_uai
    ON resultat (etablissement_uai);
