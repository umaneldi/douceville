-- Fiscalité locale des particuliers (DGFiP, data.gouv.fr), par commune et année.
-- Table créée vide ; chargée par scripts/import_fiscalite_parquet.py.
CREATE TABLE IF NOT EXISTS fiscalite (
    -- Code INSEE de la commune (colonne `INSEE COM` de la source).
    code_commune     TEXT NOT NULL REFERENCES commune (code_commune),
    -- Exercice fiscal (colonne `EXERCICE`).
    year             INTEGER NOT NULL,
    -- Taux globaux (commune + intercommunalité + …), en %.
    taux_global_tfb  DOUBLE PRECISION,
    -- NULL si la commune ne perçoit pas de TEOM.
    taux_plein_teom  DOUBLE PRECISION,
    taux_global_tfnb DOUBLE PRECISION,
    taux_global_th   DOUBLE PRECISION,
    -- Régime fiscal de l'EPCI : FPU, FPZ ou FPA ; NULL pour une commune isolée.
    optepci          TEXT,
    -- Commence par code_commune : sert aussi d'index pour la FK.
    PRIMARY KEY (code_commune, year)
);
