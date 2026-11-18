-- Délinquance enregistrée par commune (SSMSI, base statistique communale).
-- Table créée vide ; chargée par scripts/import_securite_parquet.py.
CREATE TABLE IF NOT EXISTS securite (
    code_commune    TEXT NOT NULL REFERENCES commune (code_commune),
    annee           INTEGER NOT NULL,
    indicateur      TEXT NOT NULL,
    unite_de_compte TEXT NOT NULL,
    -- NULL lorsque la donnée n'est pas diffusée (secret statistique).
    nombre          INTEGER,
    taux_pour_mille DOUBLE PRECISION,
    est_diffuse     BOOLEAN NOT NULL,
    PRIMARY KEY (code_commune, annee, indicateur)
);
