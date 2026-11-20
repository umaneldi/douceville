-- Logements locatifs des bailleurs sociaux (RPLS, « données détaillées au
-- logement », data.gouv.fr), une ligne par logement.
-- Table créée vide ; chargée par scripts/import_logement_social.py.

-- Première version de la table (coordonnées en colonnes lat, lon) : remplacée.
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_name = 'logement_social' AND column_name = 'lat') THEN
        DROP TABLE logement_social;
    END IF;
END $$;

CREATE TABLE IF NOT EXISTS logement_social (
    -- Quartier IRIS du logement (colonne `PLG_IRIS2024_CODE`) ; les IRIS
    -- fictifs ou non numériques sont filtrés à l'import.
    iris                  TEXT NOT NULL REFERENCES quartier (iris),
    -- Droit réel du bailleur (`DROIT_LIBELLE`) : pleine propriété, usufruit, …
    type_droit_immobilier TEXT,
    -- `TYPECONST_LIBELLE` : collectif, individuel, logement étudiant ; NULL si « NC ».
    type_construction     TEXT,
    -- `NBPIECE` ; NULL si « NC ».
    nb_pieces             INTEGER,
    -- Surface habitable en m² (`SURFHAB`) ; NULL si absente ou >= 10000.
    surface               INTEGER,
    -- Année de construction (`CONSTRUCT`) ; NULL si absente ou <= 1000.
    annee_construction    INTEGER,
    -- Position (lon, lat) WGS84 convertie depuis `X`, `Y` (projection `EPSG`) ;
    -- SRID 0 comme etablissement.position. NULL si non géolocalisé.
    position              geometry(Point)
);
-- Sert aussi d'index pour la FK.
CREATE INDEX IF NOT EXISTS logement_social_iris_idx ON logement_social (iris);
CREATE INDEX IF NOT EXISTS logement_social_position_gist
    ON logement_social USING GIST (position);
