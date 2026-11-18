-- Référentiel des communes actuelles (INSEE, v_commune_depuis_1943.csv).
-- Table créée vide ; chargée par scripts/import_communes_csv.py.
CREATE TABLE IF NOT EXISTS commune (
    -- PRIMARY KEY : unique et non NULL.
    code_commune  TEXT NOT NULL PRIMARY KEY,
    nom           TEXT NOT NULL,
    date_creation DATE NOT NULL
);
