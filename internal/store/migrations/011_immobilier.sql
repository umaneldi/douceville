-- Ventes immobilières DVF géolocalisées (« demandes de valeurs foncières
-- géolocalisées », data.gouv.fr), une ligne par vente d'un seul bien bâti.
-- Table créée vide ; chargée par scripts/import_immobilier_dvf.py.

-- Ancienne version de la table (prix au m² agrégés par IRIS) : remplacée.
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_name = 'immobilier' AND column_name = 'prix_m2') THEN
        DROP TABLE immobilier;
    END IF;
END $$;

CREATE TABLE IF NOT EXISTS immobilier (
    -- Quartier IRIS contenant le point (longitude, latitude) de la vente.
    iris                TEXT NOT NULL REFERENCES quartier (iris),
    date_mutation       DATE NOT NULL,
    -- Toujours « Vente » (filtré à l'import).
    nature_mutation     TEXT NOT NULL,
    valeur_fonciere     NUMERIC(15, 2) NOT NULL,
    id_parcelle         TEXT NOT NULL,
    -- NULL si absente de la source.
    surface_terrain     INTEGER,
    -- > 0 (filtré à l'import).
    surface_reelle_bati INTEGER NOT NULL
);
-- Commence par iris : sert aussi d'index pour la FK.
CREATE INDEX IF NOT EXISTS immobilier_iris_date_idx ON immobilier (iris, date_mutation);
